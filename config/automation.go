package config

// normalizeAutomation keeps an empty YAML list equal to an omitted section
// when configuration values round-trip through the editor.
func (c *Config) normalizeAutomation() {
	if len(c.Automation) == 0 {
		c.Automation = nil
	}
}
