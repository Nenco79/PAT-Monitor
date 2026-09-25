package icon

// The application manifest, the third kind of resource the executable carries.
const (
	rtManifest = 24

	// CREATEPROCESS_MANIFEST_RESOURCE_ID: the one identifier the loader reads
	// when it starts an executable. A manifest under any other number is a
	// resource nobody asks for.
	appManifestID = 1
)

// AppManifest is what Windows reads before the first line of the program runs.
//
// **It exists for the DPI declaration**, and the program already makes that
// declaration: declareDPI in internal/tray calls SetProcessDpiAwarenessContext
// before any window. The Windows App Certification Kit could not see it, since
// the call goes through a DLL loaded at run time and the kit reads the file's
// import table, so it reported the package as not DPI aware. The manifest is
// the road Microsoft documents, it takes effect before any code, and it is
// something a tool can read. The run-time call stays, for a binary built by
// hand with `go build`, which has no resource file and so no manifest.
//
// **Nothing else is declared, and that is the point of it.** An executable with
// no manifest and one with a manifest that says only this behave the same in
// every other respect: `asInvoker` is the level an unmanifested program already
// runs at, and a 64-bit process gets no UAC file virtualisation either way. A
// `compatibility` section would change what version-sensitive APIs answer, and
// a comctl32 v6 dependency would change how every common control draws; each
// is a decision of its own, and neither is taken by adding this file.
//
// `true/pm` is the older element, for the Windows versions before 1607 that do
// not read `dpiAwareness`; where both are present the newer one wins.
var AppManifest = []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
  <application xmlns="urn:schemas-microsoft-com:asm.v3">
    <windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2</dpiAwareness>
    </windowsSettings>
  </application>
</assembly>
`)
