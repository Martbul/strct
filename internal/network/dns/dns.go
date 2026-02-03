package dns

import "log"

type AdBlocker struct {
	Port string
}

// Fixes "dns.NewAdBlocker undefined"
func NewAdBlocker(port string) *AdBlocker {
	return &AdBlocker{Port: port}
}

func (a *AdBlocker) Start() error {
	log.Printf("[DNS] Starting AdBlocker on %s (Skeleton)", a.Port)
	// You will implement the actual DNS logic here later
	// For now, block forever so the program doesn't exit
	select {} 
}