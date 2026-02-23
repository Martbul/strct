package wifi

func (s *WiFi) SetState(cfg WiFiConfig)  { s.mu.Lock(); s.state = cfg; s.mu.Unlock() }
func (s *WiFi) ApplyForTest() error       { return s.apply() }
func (s *WiFi) TeardownForTest()          { s.teardown() }