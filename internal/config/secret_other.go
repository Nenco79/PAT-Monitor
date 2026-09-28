//go:build !windows

package config

// machineSecret has no source outside Windows: the name is used instead.
func machineSecret() string { return "" }
