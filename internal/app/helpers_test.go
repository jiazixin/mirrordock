package app

import "mirrordock/internal/config"

// openFile loads a configuration file and opens the application on it, as
// main does with its parsed flags.
func openFile(file string) (*App, error) {
	cfg, err := config.Open(file)
	if err != nil {
		return nil, err
	}
	return OpenConfig(cfg)
}

// reloadConfig rereads the YAML file without the runtime change checks.
func reloadConfig(m *config.Manager) error {
	return m.ReloadChecked(func(config.Config) error { return nil })
}
