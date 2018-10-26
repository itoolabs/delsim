package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
)

const (
	sampleRate    = 8000                       // per second
	sampleSize    = 2                          // bytes
	packetLength  = 20 * time.Millisecond
	packetSamples = int(sampleRate / (time.Second / packetLength))
	packetSize    = sampleSize * packetSamples
)

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

type Source interface {
	Get() []byte
	Close()
}

type Sink interface {
	Put([] byte)
	Close()
}

type debugSource struct {
	val uint8
}

func (d *debugSource) Get() []byte {
	pkt := alloc()
	for i := 0; i < len(pkt); i++ {
		pkt[i] = byte(d.val)
		d.val++
	}
	return pkt
}

func (d *debugSource) Close() {}

type nullSource struct {}

func (*nullSource) Get() []byte {
	return alloc()
}

type statSink struct {
	total int
}

func (s *statSink) Put([]byte) {
	s.total += 1
	if s.total % 500 == 0 {
		fmt.Printf("%d packets received\n", s.total)
	}
}

func (s *statSink) Close() {
	fmt.Printf("%d packets received total\n", s.total)

}

type fileSink struct {
	f *os.File
}

func newFileSink(name string) (*fileSink, error) {
	f, err := os.OpenFile(name, os.O_WRONLY | os.O_CREATE | os.O_TRUNC, 0644)
	if err != nil {
		return nil, err
	} else {
		return &fileSink{f: f}, nil
	}
}

func (s *fileSink) Put(b []byte) {
	if _, err := s.f.Write(b); err != nil {
		fmt.Printf("error writing packet to file: %q\n", err)
	}
}

func (s *fileSink) Close() {
	_ = s.f.Close()
}

type Client struct {
	conn *net.UDPConn
	addr *net.UDPAddr
	src  Source
	sink Sink
	wg   sync.WaitGroup
}

func (c *Client) recv(wg *sync.WaitGroup) {
	var (
		oob = make([]byte, 1024)
	)
Loop:
	for {
		buf := alloc()
		n, _, flags, addr, err := c.conn.ReadMsgUDP(buf, oob)
		if err != nil {
			fmt.Printf("error receiving packet: %q\n", err)
			break Loop
		} else {
			if n < packetSize || flags&syscall.MSG_TRUNC != 0 {
				fmt.Printf("invalid packet received, discarding\n")
			} else if addr.Port != c.addr.Port || !addr.IP.Equal(c.addr.IP) || addr.Zone != c.addr.Zone {
				fmt.Printf("packet from unexpected address (%s), discarding\n", addr.String())
			} else {
				c.sink.Put(buf)
				free(buf)
			}
		}
	}
	wg.Done()
}

func (c *Client) send(wg *sync.WaitGroup) {
	var (
		ts = time.Now()
		t  = time.NewTimer(0)
	)
Loop:
	for {
		select {
		case <- t.C:
			pkt := c.src.Get()
			_, err := c.conn.WriteToUDP(pkt, c.addr)
			if err != nil {
				fmt.Printf("error sending packet: %q\n", err)
				break Loop
			}
			ts = ts.Add(packetLength)
			t.Reset(ts.Sub(time.Now()))
		}
	}
	wg.Done()
}

func (c *Client) Start() {
	c.wg.Add(2)
	go c.recv(&c.wg)
	go c.send(&c.wg)
}

func (c *Client) Stop() {
	_ = c.conn.Close()
	c.wg.Wait()
	c.src.Close()
	c.sink.Close()
}

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("usage: client remote-address:port\n")
		os.Exit(1)
	}
	addr, err := net.ResolveUDPAddr("udp", os.Args[1])
	if err != nil {
		fmt.Printf("error parsing address %s: %q\n", os.Args[1], err)
		os.Exit(1)
	}
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		fmt.Printf("error opening UDP socket: %q\n", err)
		os.Exit(1)
	}
	var (
		sink Sink
		src  Source
	)
	if len(os.Args) > 3 {
		src, err = newWAVFileSource(os.Args[2])
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		sink, err = newWAVFileSink(os.Args[3])
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
	} else if len(os.Args) > 2 {
		if filepath.Ext(os.Args[2]) == ".wav" {
			sink, err = newWAVFileSink(os.Args[3])
		} else {
			sink, err = newFileSink(os.Args[2])
		}
		if err != nil {
			fmt.Printf("error opening file: %q\n", err)
			os.Exit(1)
		}
		src = &debugSource{}
	} else {
		src = &debugSource{}
		sink = &statSink{}
	}
	c := &Client{
		conn: conn,
		addr: addr,
		src: src,
		sink: sink,
	}
	go c.Start()
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	fmt.Printf("waiting for signal\n")
Loop:
	for {
		sig := <-sc
		fmt.Printf("got signal %s\n", sig)
		switch sig {
		case syscall.SIGHUP:
			break Loop
		case syscall.SIGINT:
			break Loop
		case syscall.SIGTERM:
			break Loop
		case syscall.SIGQUIT:
			data := make([]byte, 65536)
			stack := string(data[:runtime.Stack(data, true)])
			fmt.Println("STACK TRACE REQUESTED:\n", stack)
		}
	}
	signal.Stop(sc)
	c.Stop()
}
