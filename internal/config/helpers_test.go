package config

// Reload rereads the YAML file without the runtime change checks.
func (m *Manager) Reload() error {
	return m.ReloadChecked(func(Config) error { return nil })
}
