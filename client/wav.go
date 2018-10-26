package main

import (
	"fmt"
	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"io"
	"os"
	"unsafe"
)

type wavFileSource struct {
	f   *os.File
	dec *wav.Decoder
	buf *audio.IntBuffer
}

type wavFileSink struct {
	f   *os.File
	enc *wav.Encoder
	buf *audio.IntBuffer
}

func newWAVFileSource(name string) (*wavFileSource, error) {
	fd, err := os.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	dec := wav.NewDecoder(fd)
	if !dec.IsValidFile() || dec.Format().NumChannels != 1 || dec.Format().SampleRate != sampleRate {
		_ = fd.Close()
		return nil, fmt.Errorf("%s is not a valid wav file", name)
	}
	return &wavFileSource{
		f:   fd,
		dec: dec,
		buf: &audio.IntBuffer{
			Format: &format,
			SourceBitDepth: 16,
			Data: make([]int, packetSamples),
		},
	}, nil
}

var format = audio.Format{
	NumChannels: 1,
	SampleRate: sampleRate,
}

func (s *wavFileSource) Get() []byte {
	pkt := alloc()
	n, err := s.dec.PCMBuffer(s.buf)
	if err != nil {
		fmt.Printf("error reading wav file: %q\n", err)
	} else {
		if n == 0 {
			_, _ = s.dec.Seek(0, io.SeekStart)
		}
		for i := packetSamples - 1; i >= 0; i-- {
			*(*int16)(unsafe.Pointer(&pkt[i * sampleSize])) = int16(s.buf.Data[i])
		}
	}
	return pkt
}

func (s *wavFileSource) Close() {
	_ = s.f.Close()
}

func newWAVFileSink(name string) (*wavFileSink, error) {
	f, err := os.OpenFile(name, os.O_WRONLY | os.O_CREATE | os.O_TRUNC, 0644)
	if err != nil {
		return nil, err
	}
	enc := wav.NewEncoder(f, sampleRate, 16, 1, 1)
	return &wavFileSink{
		f:   f,
		enc: enc,
		buf: &audio.IntBuffer{
			Format: &format,
			SourceBitDepth: 16,
			Data: make([]int, packetSamples),
		},
	}, nil
}

func (s *wavFileSink) Put(pkt []byte) {
	for i := packetSamples - 1; i >= 0; i-- {
		s.buf.Data[i] = int(*(*int16)(unsafe.Pointer(&pkt[i * sampleSize])))
	}
	if err := s.enc.Write(s.buf); err != nil {
		fmt.Printf("error writing WAV file: %q\n", err)
	}
}

func (s *wavFileSink) Close() {
	fmt.Printf("closing sink\n")
	_ = s.enc.Close()
	_ = s.f.Close()
}