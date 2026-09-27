package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// computerNames asks Windows what it calls this machine: the NetBIOS name, the
// DNS host name and the fully qualified one. A name it cannot give is left out.
func computerNames() []string {
	var out []string
	for _, kind := range []uint32{
		windows.ComputerNameNetBIOS,
		windows.ComputerNameDnsHostname,
		windows.ComputerNameDnsFullyQualified,
	} {
		n := uint32(256)
		buf := make([]uint16, n)
		if err := windows.GetComputerNameEx(kind, &buf[0], &n); err == windows.ERROR_MORE_DATA {
			buf = make([]uint16, n)
			if windows.GetComputerNameEx(kind, &buf[0], &n) != nil {
				continue
			}
		} else if err != nil {
			continue
		}
		out = append(out, windows.UTF16ToString(buf[:n]))
	}
	return out
}

// dnsSuffixes asks Windows for the DNS suffixes the network adapters that are
// up were handed, the connection's own and its search list.
//
// **They are the router's names for this network**, and the only place a
// router's suffix can be read without asking the router: a DHCP server hands it
// to the adapter, and Windows keeps it there. The tailnet's domain is one of
// them, set by a Tailscale client on its own adapter.
//
// **No part of the answer is skipped, because skipping lost that one.**
// Measured: asked with the address and DNS-server parts left out, the Tailscale
// adapter came back with no suffix at all, and the PC's MagicDNS name was
// refused at login from another node; asked whole, it carries its domain.
func dnsSuffixes() []string {
	const flags = 0
	size := uint32(64 << 10)
	var buf []byte
	for range 3 {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, flags, 0,
			(*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil {
			break
		}
		if err != windows.ERROR_BUFFER_OVERFLOW {
			return nil
		}
		buf = nil
	}
	if buf == nil {
		return nil
	}

	var out []string
	for a := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); a != nil; a = a.Next {
		if a.OperStatus != windows.IfOperStatusUp {
			continue
		}
		if a.DnsSuffix != nil {
			out = append(out, windows.UTF16PtrToString(a.DnsSuffix))
		}
		for s := a.FirstDnsSuffix; s != nil; s = s.Next {
			out = append(out, windows.UTF16ToString(s.String[:]))
		}
	}
	return out
}
