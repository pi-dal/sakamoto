// Local endpoints for the disposable Android profile lifecycle emulator.
package main

import (
	"encoding/json"
	"github.com/miekg/dns"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

func main() {
	dns.HandleFunc(".", func(w dns.ResponseWriter, q *dns.Msg) {
		reply := new(dns.Msg).SetReply(q)
		for _, question := range q.Question {
			address := "127.0.0.2" // no port 80 listener, producing a real refused dial
			if question.Name == "explicit-direct.fixture.example." || question.Name == "regional.fixture.example." || question.Name == "unmatched.fixture.example." {
				address = "127.0.0.1"
			}
			if question.Qtype == dns.TypeA {
				reply.Answer = append(reply.Answer, &dns.A{Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 30}, A: net.ParseIP(address)})
			}
		}
		_ = w.WriteMsg(reply)
	})
	go func() { log.Fatal(dns.ListenAndServe("127.0.0.9:53", "udp", nil)) }()
	var healthA, healthB atomic.Int64
	for _, endpoint := range []struct {
		addr   string
		status int
	}{{"127.0.0.1:18584", 204}, {"127.0.0.1:80", 200}} {
		go func() {
			s := http.Server{Addr: endpoint.addr, ReadHeaderTimeout: time.Second * 3, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/health/A":
					healthA.Add(1)
				case "/health/B":
					healthB.Add(1)
				case "/counts":
					_ = json.NewEncoder(w).Encode(map[string]int64{"A": healthA.Load(), "B": healthB.Load()})
					return
				}
				w.WriteHeader(endpoint.status)
			})}
			log.Fatal(s.ListenAndServe())
		}()
	}
	select {}
}
