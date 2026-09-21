package update

// PublicKey is the ed25519 key that release archives are signed with.
//
// **Nothing reads it yet, and it ships anyway.** That is the whole reason this
// file exists now rather than later, and it is not obvious: a verifier can only
// trust a key the running binary already carries. If the constant first appeared
// in the release that introduces the download, the first version able to fetch
// and check an update would be the one *after* it, and every installation older
// than that would pay a manual hop to reach it. Shipping it now costs one file
// and a signature step in the build, and it buys that hop plus the absence of a
// second decision about which signature to trust.
//
// **That cost is one hop and this comment used to say "for ever".** It does not
// hold: the release that brings the downloader brings the real constant with it,
// whoever is older is told, fetches that one by hand, and updates by itself from
// then on — and the hop is the same one this phase asks of everybody, because
// this phase downloads nothing. The conclusion survived the correction; the
// argument for it did not, and **a cost written larger than it is buys the
// decision it was meant to support.**
//
// **What it is for, when the time comes.** TLS proves the bytes came from GitHub
// unaltered; it does not prove they are ours. Whoever takes the account publishes
// a release, and every installation downloads and runs it — and a SHA256SUMS
// published beside the archive does not help, because the same account publishes
// that too. A signature whose private half has never been near GitHub is the only
// thing in this chain that a compromised account cannot forge.
//
// **The private half never leaves the machine that builds releases.** It is not
// in this repository and must not be: `.gitignore` refuses `*.key`, and
// `build.ps1 -Release` reads it from a path given to it. The cost is declared
// rather than discovered — **lose it and no installed monitor will ever accept an
// automatic update**, because the only remedy is a new key, which can only reach
// people inside a release they would have to install by hand. That is a backup
// problem, and the backup is somebody's actual job rather than a line in a file.
//
// A second, offline rotation key is deliberately **not** kept. One key with a
// real backup is honest; two keys with one backup is theatre, and the second one
// would sit unused for years and be lost in exactly the same way.
//
// Generated with `pat-sign -generate` before the first public release, which is
// the only moment at which putting it here is free.
// Replace both halves together or neither: a public half that does not match the
// private one refuses every signature, and from outside that is indistinguishable
// from a tampered download.
var PublicKey = [32]byte{
	0xab, 0x2f, 0x14, 0x56, 0x18, 0x67, 0xd8, 0x1c,
	0xec, 0x5d, 0x57, 0x50, 0x00, 0xd5, 0xfe, 0x7d,
	0xd1, 0x0b, 0x4a, 0x45, 0xc1, 0x1d, 0xca, 0xfd,
	0x22, 0xf3, 0x4d, 0x4c, 0x1c, 0xe4, 0x6a, 0xd9,
}

// IsSigned says whether a real key has been put here.
//
// **An all-zero key is not a key**, and the distinction has to be drawn
// somewhere: a verifier handed one would refuse every signature, which from
// outside looks exactly like a tampered download — the alarming diagnosis for
// the harmless cause. Whoever writes the download half asks this first and
// declines to check at all rather than failing loudly for the wrong reason.
func IsSigned() bool {
	for _, b := range PublicKey {
		if b != 0 {
			return true
		}
	}
	return false
}
