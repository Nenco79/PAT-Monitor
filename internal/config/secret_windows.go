package config

import "golang.org/x/sys/windows/registry"

// machineSecret is the machine's GUID, which Windows generates at installation
// and nobody outside it can guess; empty if it cannot be read.
func machineSecret() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`,
		registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue("MachineGuid")
	if err != nil {
		return ""
	}
	return v
}
