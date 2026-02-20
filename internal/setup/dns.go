package setup

import (
	"fmt"
	"log/slog"

	"github.com/miekg/dns"
)

func StartDNSServer(redirectIP, addr string) *dns.Server {
	dns.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true

		for _, q := range r.Question {
			if q.Qtype == dns.TypeA {
				// Redirect ALL A-record requests to our IP
				rr, _ := dns.NewRR(fmt.Sprintf("%s 3600 IN A %s", q.Name, redirectIP))
				m.Answer = append(m.Answer, rr)
			}
		}
		w.WriteMsg(m)
	})

	server := &dns.Server{Addr: addr, Net: "udp"}

	go func() {
		slog.Info("Starting DNS Spoofing Server", "addr", addr, "redirectIP", redirectIP)
		if err := server.ListenAndServe(); err != nil {
			slog.Error("Failed to start DNS server", "err", err)
		}
	}()

	return server
}
