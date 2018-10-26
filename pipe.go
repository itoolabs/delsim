package main

import (
	"fmt"
	"github.com/zaf/g711"
	"net"
	"sync"
	"time"
	"unsafe"
)

const (
	sampleRate    = 8000                       // per second
	sampleSize    = 2                          // bytes
	packetLength  = 20 * time.Millisecond
	packetSamples = int(sampleRate / (time.Second / packetLength))
	packetSize    = sampleSize * packetSamples
)

type endpoint struct {
	name   string
	conn   *net.UDPConn
	remote *net.UDPAddr
	in     chan[]byte
	out    chan[]byte
}

type packet struct {
	ts   time.Time
	src  *net.UDPAddr
	data []byte
}

var (
	bac = make(chan []byte, 16)
)

func alloc() []byte {
	var ret []byte
	select {
	case ret = <- bac:
		for i := range ret {
			ret[i] = 0
		}
	default:
		ret = make([]byte, packetSize)
	}
	return ret
}

func free(b []byte) {
	select {
	case bac <- b:
	default:
	}
}


func newEndpoint(name string, conn *net.UDPConn) *endpoint {
	return &endpoint{
		name: name,
		conn: conn,
		in:   make(chan[]byte),
		out:  make(chan[]byte),
	}
}

type queue struct {
	pkts [][]byte
	cur  int
	rdr  int
	off  int
}

func newQueue(delay time.Duration) *queue {
	assert(delay > packetLength, "delay (%s) must be > %s", delay, packetLength)
	sz := int(delay / packetLength) + 3
	var off int
	if delay % packetLength != 0 {
		off = sampleSize * int(sampleRate/(time.Second/(delay%packetLength)))
	}
	debugf("new queue len = %d packets, off = %d", sz, off)
	ret := &queue{
		pkts: make([][]byte, sz),
		off:  off,
	}
	if off > 0 {
		ret.pkts[0] = make([]byte, packetSize)
	}
	return ret
}

func (q *queue) enqueue(pkt []byte) {
	assert(len(pkt) == packetSize, "invalid packet size in enqueue: %d", len(pkt))
	nxt := (q.cur + 1) % len(q.pkts)
	// debugf("enqueuing packet at %d (%d), nxt = %d", q.cur, q.off, nxt)
	if q.off == 0 {
		q.pkts[q.cur] = pkt
	} else {
		copy(q.pkts[q.cur][q.off:], pkt[:q.off])
		copy(pkt, pkt[q.off:])
		q.pkts[nxt] = pkt
	}
	q.cur = nxt
}

func (q *queue) dequeue() ([]byte, bool) {
	if q.rdr == q.cur {
		return nil, false
	}
	nxt := (q.rdr + 1) % len(q.pkts)
	ret := q.pkts[q.rdr]
	q.pkts[q.rdr] = nil
	q.rdr = nxt
	return ret, true
}

func mangle(pkt []byte) {
	for i := packetSamples - 1; i >= 0; i-- {
		frmPtr := (*int16)(unsafe.Pointer(&pkt[i*sampleSize]))
		*frmPtr = g711.DecodeAlawFrame(g711.EncodeAlawFrame(*frmPtr))
	}
}

func delay(name string, wg *sync.WaitGroup, delay time.Duration, in <- chan []byte, out chan <- []byte) {
	var (
		t = time.NewTimer(0)
		q = newQueue(delay)
		ts = time.Now()
	)
	debugf("%s delayer started", name)
Loop:
	for {
		select {
		case pkt, ok := <- in:
			if !ok {
				break Loop
			}
			mangle(pkt)
			q.enqueue(pkt)
		case <-t.C:
			ts = ts.Add(packetLength)
			if pkt, ok := q.dequeue(); ok {
				out <- pkt
			} else {
				out <- alloc()
			}
			t.Reset(ts.Sub(time.Now()))
		}
	}
	wg.Done()
}

func process(d time.Duration, left, right *endpoint) {
	var wg sync.WaitGroup
	go left.work()
	go right.work()
	wg.Add(2)
	go delay(fmt.Sprintf("%s->%s", left.name, right.name), &wg, d, left.in, right.out)
	go delay(fmt.Sprintf("%s->%s", right.name, left.name), &wg, d, right.in, left.out)
	debugf("started process with delay %s", d.String())
	wg.Wait()
}

func (ep *endpoint) receive(ch chan packet) {
	var (
		oob = make([]byte, 1024)
	)
	debugf("%s endpoint receiver started", ep.name)
	for {
		buf := alloc()
		n, _, _, addr, err := ep.conn.ReadMsgUDP(buf, oob)
		if err != nil {
			logf("%s error receiving packet: %q", ep.name, err)
			break
		} else {
			ts := time.Now()
			if n < packetSize {
				logf("%s packet from %s discarded due to unexpected length", ep.name, addr.String())
			} else if addr.Port <= 1024 || addr.Port > 65533 {
				logf("%s packet from %s discarded due to unexpected port (must be 1024 < port < 65534)", ep.name, addr.String())
			} else {
				ch <- packet{
					src: addr,
					data: buf,
					ts:   ts,
				}
			}
		}
	}
}

func (ep *endpoint) work() {
	var (
		to = time.NewTimer(0)
		ts time.Time
		ch = make(chan packet)
	)
	go ep.receive(ch)
	debugf("%s endpoint worker started", ep.name)
Loop:
	for {
		select {
		case pkt, ok := <-ch:
			if !ok {
				break Loop
			}
			if ep.remote == nil {
				debugf("%s received first packet from %s", ep.name, pkt.src.String())
				ep.remote = pkt.src
				ts = pkt.ts.Add(packetLength)
			} else if ep.remote.Port != pkt.src.Port || !ep.remote.IP.Equal(pkt.src.IP) {
				logf("%s dumping packets coming from unexpected source %s (expected %s)", ep.name, pkt.src.String(), ep.remote.String())
				continue Loop
			} else {
				tdiff := pkt.ts.Sub(ts)
				if tdiff < -(packetLength / 2) {
					logf("%s discarding packet coming too early: %s", ep.name, tdiff.String())
					continue Loop
				}
				ts = ts.Add(packetLength)
			}
			if !to.Stop() {
				select {
				case <-to.C:
				default:
				}
			}
			to.Reset(ts.Add(packetLength / 4).Sub(time.Now()))
			ep.in <- pkt.data
		case <-to.C:
			if ep.remote != nil {
				logf("%s timeout waiting for packet, unbinding remote %s", ep.name, ep.remote.String())
				ep.remote = nil
			}
		case pkt := <- ep.out:
			if ep.remote != nil {
				if _, err := ep.conn.WriteToUDP(pkt, ep.remote); err != nil {
					logf("%s error sending packet: %q", ep.name, err)
				}
				free(pkt)
			}
		}
	}
	close(ep.in)
}

