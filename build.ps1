# Builds PAT Monitor and the diagnostic tools.
#
# It exists for a reason that cannot be guessed by looking at the code: the
# version the node declares to the Tailscale control panel has to be stamped
# here, with the linker.
#
# tailscale.com/version derives it from debug.ReadBuildInfo(), reading the
# commit's revision and date. Those fields are missing more often than one would
# think — building from a tree with no commit, from a zip downloaded from GitHub,
# or from a shallow copy — and when they are, the library discards the
# information and falls back to "1.102.1-ERR-BuildInfo", which in the node list
# reads as a broken client. The stamps are the intended road: it is what
# build_dist.sh does in Tailscale's own source.

# **The default is the shape the monitor is installed in**: no console
# (-H=windowsgui), stripped, with the notification-area icon as its only
# presence. Whoever builds without asking for anything gets what is shipped, and
# whoever is measuring says so with `-Console`.
#
# It holds for the monitor only: the diagnostic tools **are** their console, and
# with the default they are not rebuilt at all.
# **-Release makes the thing that gets published**, which until now was
# assembled by hand: the stripped binary plus LICENSE, NOTICE and licenses\,
# zipped and signed.
#
# The licence tree travels because it has to. Apache-2.0 section 4 and the
# BSD-3 of libopus both require the notices to go with a redistribution **in
# binary form**, so the archive — and not the bare .exe the README used to point
# at — is the thing that may legally be handed to somebody. Assembling it by
# hand is how that tree goes stale next to a newer binary, silently.
#
# The signature exists for the download half that is not written yet, and it is
# made now so that the release stream is already signed when it is: TLS proves
# the bytes came from GitHub unaltered, not that they are ours, and a checksum
# published beside the archive is published by the same account. The key comes
# from -Key or from PATMON_SIGNING_KEY, and it is not in this repository.
#
# **-Unsigned is for the build that cannot have the key, which is the one in
# CI**, and it is a switch rather than a fallback on purpose. The key's whole
# job is to survive this GitHub account being taken, so a key reachable from a
# workflow would prove the thing it exists to disprove; the archive is therefore
# built there and signed here, on the machine that holds the half nobody else
# has. What must not happen is an unsigned release going out by **omission** —
# the one made in a hurry, with nobody noticing — so silence still refuses, and
# only this word gets a zip without a signature. What comes out is declared
# unsigned in the line it prints, and the release it belongs to stays a draft
# until the .sig is beside it.
param([switch]$Console, [switch]$Release, [switch]$Msix, [switch]$Unsigned, [string]$Key)

$ErrorActionPreference = 'Stop'

$env:Path = "C:\Program Files\Go\bin;$env:Path"
$env:CGO_ENABLED = '0'          # a single static binary, no runtime to install

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Push-Location $root
try {
    # **-Release is the shipped shape, so it refuses -Console**, and it refuses
    # it here rather than at the packaging step. Together the two flags publish
    # a binary with a console window and no stripping — a black rectangle on
    # somebody's screen at start-up, and 12 MB nobody needs — and neither shows
    # in the archive's name. Checked further down it still refuses, but only
    # after `go build` has already put a console binary in bin\, where the next
    # plain run of this script is what puts it back: a refusal that leaves the
    # wrong thing behind is half a refusal.
    if ($Release -and $Console) { throw "-Release builds what ships: use it without -Console" }

    # -Unsigned says something about a release and nothing about any other
    # build, so on its own it is a word that moves nothing — and a flag that
    # moves nothing is read by whoever typed it as a flag that worked.
    if ($Unsigned -and -not $Release) { throw "-Unsigned only says anything about -Release" }

    # -Msix builds the shape that goes inside a package, so it refuses -Console
    # for the reason -Release does, and it refuses a dirty tree further down for
    # the reason -Release does: what comes out of here is meant to be submitted.
    if ($Msix -and $Console) { throw "-Msix builds what ships: use it without -Console" }

    # **And -Release with it would sign the wrong binary.** There is one
    # `go build`, so the stamp that switches the update check off for ever lands
    # in the executable the release block then zips and signs: a standalone
    # archive whose owner is never told a fix exists, and whose `-show-config`
    # prints "off, the Store updates this build" over a machine no Store will
    # ever update. The two shapes are built one at a time, and the refusal is
    # here rather than further down for the reason written above the first one:
    # a refusal that leaves the wrong thing in bin\ is half a refusal.
    if ($Msix -and $Release) { throw "-Msix and -Release are two shapes: build them one at a time" }

    # Rebuilding over a running process, Windows cannot overwrite the
    # executable: it **renames** it to .exe~ and puts the new one in its place.
    # That is 43 MB a time and none of them goes away on its own. The one of the
    # instance still running stays locked: it goes on the next round, and that is
    # why the error here is ignored instead of stopping the build.
    Get-ChildItem bin\*.exe~ -ErrorAction SilentlyContinue |
        ForEach-Object { Remove-Item $_.FullName -Force -ErrorAction SilentlyContinue }

    # The version is asked of the module instead of being repeated here,
    # otherwise at the first update of tailscale.com the node would declare a
    # version that is not the one of the code that is running.
    $ts = (& go list -m -f '{{.Version}}' tailscale.com) -replace '^v', ''
    if (-not $ts) { throw "tailscale.com version not determined" }

    # The stamp's suffix is cosmetic — it appears in the node list as
    # "1.102.2-patmonitor" — and has nothing to do with the hostname, which
    # generates the public URL and is distinct per machine.
    $ldflags = "-X tailscale.com/version.longStamp=$ts-patmonitor " +
               "-X tailscale.com/version.shortStamp=$ts"

    # --- our own version ---
    #
    # The revision **grows on its own**, and git counts it rather than a file in
    # the repository: a counter in a file would collide at every merge and would
    # give different numbers on different machines, which are the two things an
    # identifier must not do. `rev-list --count` is monotonic, requires nothing
    # to be remembered, and two builds from the same commit carry the same number
    # — which is right, because they are the same code.
    #
    # What that count does not cover is a dirty tree: building with uncommitted
    # changes, the binary corresponds to no commit. That is declared, instead of
    # letting one believe the opposite precisely while testing something.
    #
    # If git is not there, or this is not a repository, it does not fail: it
    # builds all the same and the binary says so. It is the same situation that
    # produced "1.102.1-ERR-BuildInfo" in the node list — with the difference
    # that there it was the library discarding the information in silence.
    $rev = & git rev-list --count HEAD 2>$null
    if ($LASTEXITCODE -ne 0 -or -not $rev) { $rev = "0" }
    $sha = & git rev-parse --short HEAD 2>$null
    # Empty and not a word: the "I do not know" signal is carried by the absence,
    # and `version.Full` recognises it by comparing with the empty string. A word
    # here and another one there is the pair that comes apart without saying so.
    if ($LASTEXITCODE -ne 0 -or -not $sha) { $sha = "" }
    $dirty = ""
    $pending = & git status --porcelain 2>$null
    if ($LASTEXITCODE -eq 0 -and $pending) { $dirty = "yes" }

    $ldflags += " -X patmonitor/internal/version.Revision=$rev" +
                " -X patmonitor/internal/version.Commit=$sha" +
                " -X patmonitor/internal/version.Modified=$dirty"

    # **The binary that goes inside a package is a different binary**, and this
    # stamp is the whole of the difference: there the updating is the Store's,
    # so the daily question to GitHub is not asked at all. A stamp and not a
    # configuration key, because the requirement is "never" - a key is something
    # a reader turns back on, and a changed default is something an existing
    # configuration file carries straight past.
    if ($Msix) { $ldflags += " -X patmonitor/internal/version.Packaged=yes" }

    $mode = "console build"
    if (-not $Console) {
        $ldflags += " -H=windowsgui"
        $mode = "GUI build, tray only"

        # --- the binary to be distributed is stripped ---
        #
        # -s -w remove the symbol table and the DWARF: 12.2 MB out of 41.9, that
        # is 29%. **It costs nothing at run time, and the reason is that those
        # sections are not loaded at all** — the loader maps .text, .rdata and
        # .data, and the rest is read only by a debugger opening the file at
        # rest. Measured: 36 ms of startup against 37, inside the noise over
        # three rounds of twenty runs.
        #
        # **Panic traces stay whole**, verified and not deduced: Go's runtime
        # builds them from its pclntab, which is in .rdata, not from the DWARF.
        # Function names and line numbers are identical in the two binaries. That
        # matters more than the size: the log on file exists so that a process
        # that exits at night leaves an explanation.
        #
        # What is lost is delve's and gdb's attach, and for that one rebuilds
        # with -Console. And that is why the stripping is here and not always:
        # while one is measuring, the whole binary is the right one.
        $ldflags += " -s -w"
        $mode += ", stripped"
    }

    # **The stamp is declared in the line, and it has to be added after the two
    # branches above rather than before them.** Set first, it was overwritten by
    # the GUI branch and the label never said it — an announcement that cannot
    # fire, over a binary left in bin\ that looks like every other and asks
    # nobody about updates for ever. The next plain run of this script puts a
    # normal one back; until it does, this word is the only thing that says so.
    if ($Msix) { $mode += ", packaged, no update check" }

    # --- the executable's icon and version ---
    #
    # They are regenerated at every build instead of living in the repository,
    # which is consistent with .gitignore, which excludes the .syso files: that
    # file is not written by hand, it derives entirely from the drawing and from
    # the numbers below, which are code instead. Given the same arguments two
    # generations produce the same bytes, so redoing it dirties nothing and there
    # are no two copies that can diverge.
    #
    # The three values are the same as the linker's stamps, and have to stay so:
    # the file's properties read what the monitor writes in the log. They have to
    # be passed because `go run` runs **before** the link, so from inside that
    # program the stamps do not exist yet.
    #
    # It holds for the monitor only: the diagnostic tools live in a console and
    # nobody looks at an icon or a details sheet of theirs.

    # **A .syso left behind does not complain: it makes the link fail.** An
    # executable has one resource section, so two .syso in the same folder give
    # "too many .rsrc sections", which names no file and does not say where it
    # comes from. They are ignored by git, so the tree looks clean and nothing is
    # visible. Renaming the generator is enough to leave two of them on every
    # machine that had built before, so they are removed here rather than by
    # hand.
    Remove-Item -Force -ErrorAction SilentlyContinue .\cmd\pat-monitor\*.syso

    $iconArgs = @("-arch", "amd64", "-rev", $rev, "-commit", $sha)
    if ($dirty) { $iconArgs += "-modified" }
    & go run .\cmd\pat-icon @iconArgs | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "icon generation failed" }

    # The number is asked of whoever holds it, instead of being read with a
    # regular expression: `version.Number` decides its own format, and a second
    # reader written here would break in silence at a change like `0.6.0` to
    # `1.0.0-beta.1`. Without it this line said `pat-monitor  r192 (...)` — that
    # is, the build did not declare which version it had just produced.
    $number = (& go run .\cmd\pat-icon -print-version).Trim()
    if ($LASTEXITCODE -ne 0) { throw "version lookup failed" }

    $label = "$number r$rev ($sha"
    if ($dirty) { $label += ", modified" }
    $label += ")"
    Write-Host "pat-monitor  $label  (Tailscale node $ts-patmonitor, $mode)"
    # -trimpath gains almost nothing in size: it removes from the binary the
    # absolute paths of the machine that built it, which in a file destined for
    # somebody else is information too many. Panic traces stay readable, with the
    # path relative to the module instead of to the disk.
    & go build -trimpath -ldflags $ldflags -o bin\pat-monitor.exe .\cmd\pat-monitor
    if ($LASTEXITCODE -ne 0) { throw "PAT Monitor build failed" }

    if ($Release) {
        # **A release built from a dirty tree matches no commit**, and the whole
        # identifier argument in internal/version rests on that not happening:
        # `r` names a commit, and a binary carrying one it does not correspond to
        # is an identifier that lies exactly when somebody is trying to find out
        # what they are running. It is cheap to refuse here and impossible to
        # correct once the archive is on the Internet.
        if ($dirty) { throw "the tree has uncommitted changes: a release must match a commit" }
        if (-not $sha) { throw "no commit: a release has to be identifiable" }

        # The licence tree is regenerated rather than trusted. It is derived
        # from what is linked into the binary, so it goes stale on any `go get`
        # — and the folder is committed, which is the combination that ages
        # without a word. licenses_test.go guards it both ways at `go test`, and
        # this makes the published copy the one that was just checked.
        & go run .\cmd\pat-licenses | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "licence collection failed" }

        $stage = "dist\PAT-Monitor-$number-windows-amd64"
        if (Test-Path $stage) { Remove-Item -Recurse -Force $stage }
        New-Item -ItemType Directory -Force $stage | Out-Null

        # **The layout is the shape of an installation**, not a folder of
        # convenience: whoever unpacks this has the thing as it is meant to sit
        # on disk, and the download half, when it is written, maps one onto the
        # other with nothing to translate.
        Copy-Item bin\pat-monitor.exe $stage
        Copy-Item LICENSE, NOTICE $stage
        Copy-Item -Recurse licenses $stage

        $zip = "$stage.zip"
        if (Test-Path $zip) { Remove-Item -Force $zip }
        # **The folder goes inside the zip, and `\*` is what left it out.**
        # Without it "Extract Here" — which is what most people press — scatters
        # the executable, LICENSE, NOTICE and the whole licences tree loose into
        # the Downloads folder, among everything else already there. The layout
        # this block stages is the shape an installation has on disk, and it has
        # to survive the unpacking or it was never a layout.
        Compress-Archive -Path $stage -DestinationPath $zip
        Write-Host "release: $zip"

        # **The signature is not optional-by-omission.** Left to a flag somebody
        # remembers, the one release that goes out unsigned is the one made in a
        # hurry — and an installation that has learned to accept unsigned
        # archives has learned it for ever. Without a key this says so and stops,
        # rather than publishing something that looks finished. -Unsigned is the
        # one way past it, and it is a word somebody typed rather than a key
        # somebody forgot.
        Remove-Item -Recurse -Force $stage
        if ($Unsigned) {
            # No em dash in this string, and that is not a style choice: this
            # file has no BOM, Windows PowerShell reads it as ANSI, and a
            # multi-byte character inside a **string** loses the parser its
            # terminator — measured, "missing string terminator". In a comment
            # it is harmless, which is why the rest of this file is full of them
            # and only a string can be broken by one.
            Write-Host "release: $(Split-Path -Leaf $zip) is NOT SIGNED"
            Write-Host "sign it where the key is, then upload both:"
            Write-Host "  go run .\cmd\pat-sign -key <path> $(Split-Path -Leaf $zip)"
        }
        else {
            $keyPath = $Key
            if (-not $keyPath) { $keyPath = $env:PATMON_SIGNING_KEY }
            if (-not $keyPath) {
                throw "no signing key: pass -Key or set PATMON_SIGNING_KEY (pat-sign -generate makes one), or -Unsigned if this build cannot hold it"
            }
            & go run .\cmd\pat-sign -key $keyPath $zip
            if ($LASTEXITCODE -ne 0) { throw "signing failed" }

            Write-Host "upload both: $(Split-Path -Leaf $zip) and $(Split-Path -Leaf $zip).sig"
        }
    }

    if ($Msix) {
        # A package is submitted, so the same two refusals as a release: a
        # binary that matches no commit is an identifier that lies exactly when
        # somebody is trying to find out what they are running.
        if ($dirty) { throw "the tree has uncommitted changes: a package must match a commit" }
        if (-not $sha) { throw "no commit: a package has to be identifiable" }

        & go run .\cmd\pat-licenses | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "licence collection failed" }

        # **The SDK is looked for and not written down.** The version folder
        # under Windows Kits changes with every SDK, and on a CI image it is
        # whatever that image happens to carry: a path spelled here is a build
        # that works on this machine and nowhere else, which is the kind of
        # failure that shows up only in somebody else's log.
        $found = Get-ChildItem "${env:ProgramFiles(x86)}\Windows Kits\10\bin\*\x64\makeappx.exe" -ErrorAction SilentlyContinue |
                 Sort-Object FullName | Select-Object -Last 1
        if (-not $found) { throw "makeappx.exe not found: the Windows SDK is not installed" }
        $sdk = Split-Path -Parent $found.FullName
        $makeappx = Join-Path $sdk "makeappx.exe"
        $makepri = Join-Path $sdk "makepri.exe"
        if (-not (Test-Path $makepri)) { throw "makepri.exe not found beside makeappx.exe" }

        # The version a manifest carries is asked of the code that owns the
        # shape of the number. It is not the one in the file properties: the
        # fourth field belongs to the Store, so the commit count is not in it.
        $pkgver = (& go run .\cmd\pat-icon -print-package-version).Trim()
        if ($LASTEXITCODE -ne 0 -or -not $pkgver) { throw "the package version could not be asked for" }

        $pkg = Join-Path "dist" "PAT-Monitor-$pkgver-x64"
        if (Test-Path $pkg) { Remove-Item -Recurse -Force $pkg }
        New-Item -ItemType Directory -Force (Join-Path $pkg "Assets") | Out-Null

        # The same layout as the archive, which is the shape an installation has
        # on disk, plus the two things only a package carries.
        Copy-Item bin\pat-monitor.exe $pkg
        Copy-Item LICENSE, NOTICE $pkg
        Copy-Item -Recurse licenses $pkg

        & go run .\cmd\pat-icon -png (Join-Path $pkg "Assets") -o (Join-Path $env:TEMP "pat-icon-msix.syso") | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "the package logos could not be drawn" }

        (Get-Content packaging\AppxManifest.xml -Raw) -replace '\{VERSION\}', $pkgver |
            Set-Content (Join-Path $pkg "AppxManifest.xml") -Encoding UTF8

        # **Without the index the logos below 44 are dead weight.** Measured: a
        # package carrying the target sizes and no resources.pri gives back an
        # image identical, pixel for pixel, to one that does not carry them at
        # all. The resource manager is what chooses among them, and it reads
        # this file.
        $priconfig = Join-Path $pkg "priconfig.xml"
        & $makepri createconfig /cf $priconfig /dq en-US /o | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "the resource config could not be created" }
        & $makepri new /pr $pkg /cf $priconfig /of (Join-Path $pkg "resources.pri") /o | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "the resource index could not be built" }
        # It is an input to the index and must not travel inside the package.
        Remove-Item -Force $priconfig

        # **Not `$msix`**: PowerShell does not distinguish case, so that name is
        # the `-Msix` switch, and assigning a path to it fails with a type error
        # naming neither the switch nor the line that meant no harm.
        $outPkg = "$pkg.msix"
        if (Test-Path $outPkg) { Remove-Item -Force $outPkg }
        & $makeappx pack /d $pkg /p $outPkg /o | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "the package could not be built" }
        Remove-Item -Recurse -Force $pkg

        # The listing's 300x300 tile is not in the package, since the manifest
        # does not name it: it is uploaded to Partner Center by hand. It is drawn
        # here, beside the package, so that each package comes with the tile of
        # the same drawing. See icon.StoreTile.
        $tile = Join-Path "dist" "PAT-Monitor-$pkgver-store-tile-300.png"
        & go run .\cmd\pat-icon -store $tile -o (Join-Path $env:TEMP "pat-icon-msix.syso") | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "the Store tile could not be drawn" }
        Write-Host "package: $outPkg"
        Write-Host "Store tile: $tile (for the listing, not inside the package)"

        # **What comes out is not signed, and that is not an omission here.** A
        # package submitted to the Store is re-signed by the Store with a
        # Microsoft certificate, so a signature made here would be replaced. The
        # one it is worth making is for sideloading, and that wants a
        # certificate this repository does not hold.
        Write-Host "unsigned: the Store signs it, and sideloading wants a certificate of your own"
    }

    # Without -Console what is being prepared is what gets shipped, and there the
    # diagnostic tools have no part: they are consoles nobody installs next to
    # the monitor, and rebuilding them costs half the time of every round. They
    # are skipped, **and it is said**: the ones left in bin\ are from an earlier
    # round, and an old tool next to a new monitor is the quickest way of
    # measuring the wrong version.
    if (-not $Console) {
        Write-Host "diagnostic tools: skipped. Use -Console to rebuild them."
    }
    else {
        # The tools do not embed tsnet: no stamps to impress. And no -s -w:
        # they **are** the place one looks when something does not add up, so
        # they stay whole and attachable from a debugger.
        foreach ($tool in 'pat-capture', 'pat-diag', 'pat-opus', 'pat-sounds', 'pat-viewer', 'pat-wasapi') {
            Write-Host $tool
            & go build -o "bin\$tool.exe" ".\cmd\$tool"
            if ($LASTEXITCODE -ne 0) { throw "$tool build failed" }
        }
    }
}
finally {
    Pop-Location
}
