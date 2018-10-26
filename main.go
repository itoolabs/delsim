package main

import (
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	"log"
	"math/rand"
	"net"
	"os"
	"time"
)

var aliceConn, bobConn *net.UDPConn

var (
	rootCmd = &cobra.Command{
		Use: "delsim",
		Short: "a simple tool to simulate phone network delays",
		Long: `delsim is a simple tool to simulate phone network delays:

delsim --alice :9001 --bob :9002

will accept a stream of UDP packets containing 20µs of 8000 Hz 16-bit signed LE PCM on
udp port :9001 and pass it to client, sending stream to port :9002, and vice verse.

`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if globals.alice.empty() || globals.bob.empty() {
				return errors.New("both alice and bob endpoints must be set")
			}
			var err error
			aliceConn, err = net.ListenUDP(globals.alice.Network(), globals.alice.UDPAddr)
			if err != nil {
				return fmt.Errorf("error opening alice endpoint: %q", err)
			}
			bobConn, err = net.ListenUDP(globals.bob.Network(), globals.bob.UDPAddr)
			if err != nil {
				return fmt.Errorf("error opening bob endpoint: %q", err)
			}
			return nil
		},
		Run: func(cmd *cobra.Command, args []string) {
			alice := newEndpoint("alice", aliceConn)
			bob := newEndpoint("bob", bobConn)
			rand.Seed(time.Now().UnixNano())
			delay := time.Duration(rand.Int63n(48) + 2) * packetLength
			process(delay, alice, bob)
		},
	}
	globals = struct {
		alice endpointAddress
		bob   endpointAddress
		debug bool
	}{}
)

type endpointAddress struct {
	*net.UDPAddr
}

func (i *endpointAddress) empty() bool {
	return i.UDPAddr == nil
}

func (i *endpointAddress) String() string {
	if i.UDPAddr == nil {
		return ""
	} else {
		return i.UDPAddr.String()
	}
}
func (i *endpointAddress) Set(s string) error {
	addr, err := net.ResolveUDPAddr("udp", s)
	if err != nil {
		return fmt.Errorf("failed to parse endpoint address: %q", err)
	}
	i.UDPAddr = addr
	return nil
}

func (i *endpointAddress) Type() string {
	return "endpointAddress"
}

func logf(f string, args ...interface{}) {
	log.Printf(f, args...)
}

func debugf(f string, args ...interface{}) {
	if globals.debug {
		log.Printf(f, args...)
	}
}
func init() {
	log.SetPrefix("delsim ")
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	rootCmd.PersistentFlags().BoolVarP(&globals.debug, "debug", "d", false, "enable debug logging")
	rootCmd.PersistentFlags().VarP(&globals.alice, "alice", "a", "alice `endpoint` (addr:port)")
	rootCmd.PersistentFlags().VarP(&globals.bob, "bob", "b", "bob `endpoint` (addr:port)")
}

func main() {
	if rootCmd.Execute() != nil {
		os.Exit(1)
	}
}