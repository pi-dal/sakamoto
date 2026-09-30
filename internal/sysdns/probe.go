package sysdns

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"golang.org/x/net/dns/dnsmessage"
	"io"
	"net"
	"time"
)

// PortsFree performs a preflight only; it never changes system DNS.
func PortsFree(address string) error {
	tcp, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("TCP DNS listener conflict: %w", err)
	}
	defer func() { _ = tcp.Close() }()
	udp, err := net.ListenPacket("udp", address)
	if err != nil {
		return fmt.Errorf("UDP DNS listener conflict: %w", err)
	}
	return udp.Close()
}

// Probe requires a real DNS answer over both UDP and TCP, without involving
// the system resolver or trusting merely that a port is open.
func Probe(address string) error {
	for _, network := range []string{"udp", "tcp"} {
		if err := probeOne(address, network); err != nil {
			return fmt.Errorf("native DNS %s probe: %w", network, err)
		}
	}
	return nil
}
func probeOne(address, network string) error {
	id := make([]byte, 2)
	if _, err := rand.Read(id); err != nil {
		return err
	}
	query := []byte{id[0], id[1], 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1}
	conn, err := net.DialTimeout(network, address, 2*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	var answer []byte
	if network == "tcp" {
		packet := append([]byte{0, byte(len(query))}, query...)
		if _, err := conn.Write(packet); err != nil {
			return err
		}
		var size [2]byte
		if _, err := io.ReadFull(conn, size[:]); err != nil {
			return err
		}
		length := int(binary.BigEndian.Uint16(size[:]))
		if length < 12 || length > 8192 {
			return fmt.Errorf("invalid DNS response length")
		}
		answer = make([]byte, length)
		if _, err := io.ReadFull(conn, answer); err != nil {
			return err
		}
	} else {
		if _, err := conn.Write(query); err != nil {
			return err
		}
		packet := make([]byte, 8192)
		n, err := conn.Read(packet)
		if err != nil {
			return err
		}
		answer = packet[:n]
	}
	var message dnsmessage.Message
	if err := message.Unpack(answer); err != nil {
		return fmt.Errorf("malformed DNS response: %w", err)
	}
	if message.ID != binary.BigEndian.Uint16(id) || !message.Response || message.RCode != dnsmessage.RCodeSuccess || len(message.Questions) != 1 || message.Questions[0].Name.String() != "example.com." {
		return fmt.Errorf("DNS response did not match the health query")
	}
	for _, resource := range message.Answers {
		if _, ok := resource.Body.(*dnsmessage.AResource); ok {
			return nil
		}
	}
	return fmt.Errorf("DNS health response did not contain an IPv4 answer")
}
