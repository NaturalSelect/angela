package config

import "slices"

// AddRuntimeMCP registers MCP servers for this process only. They appear in
// Config().MCP like any configured server, survive a reload, and are never
// written to disk. It returns the names it registered, sorted.
//
// NOTE: A name that a config file defines is left alone, so an editor cannot
// replace what the user wrote. A name an earlier call registered is replaced.
func (s *ConfigStore) AddRuntimeMCP(servers map[string]MCPConfig) []string {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	nc := s.Config().cloneForWrite()
	if nc.MCP == nil {
		nc.MCP = make(map[string]MCPConfig)
	}
	var added []string
	for name, m := range servers {
		_, runtime := s.overrides.MCP[name]
		if _, exists := nc.MCP[name]; exists && !runtime {
			continue
		}
		if s.overrides.MCP == nil {
			s.overrides.MCP = make(map[string]MCPConfig)
		}
		s.overrides.MCP[name] = m
		nc.MCP[name] = m
		added = append(added, name)
	}
	s.setConfig(nc)
	slices.Sort(added)
	return added
}

// IsRuntimeMCP reports whether name was registered with AddRuntimeMCP and is
// not defined by a config file.
func (s *ConfigStore) IsRuntimeMCP(name string) bool {
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	_, ok := s.overrides.MCP[name]
	return ok
}

// reapplyRuntimeMCP merges the runtime servers into a freshly loaded cfg.
// Caller must hold writeMu.
//
// NOTE: A config file that now defines a name wins, and the runtime entry is
// dropped so the server is no longer treated as editor-supplied.
func (s *ConfigStore) reapplyRuntimeMCP(cfg *Config) {
	for name, m := range s.overrides.MCP {
		if _, inFile := cfg.MCP[name]; inFile {
			delete(s.overrides.MCP, name)
			continue
		}
		cfg.MCP[name] = m
	}
}
