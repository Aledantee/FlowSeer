// Package identity is what an edge agent is: the Ed25519 key pair central
// registered, the enrollment answer that names it, and the signer every call
// except Enroll needs.
//
// The write order is the load-bearing part, and it is not symmetric. The key
// pair is persisted before Enroll is called, and the response is persisted
// after it returns. Each crash point in that order recovers — the setup key
// is still unconsumed until the call lands, and central answers a repeat of
// the same key with the same response. Reversed, one crash point has no way
// back: central would hold a public key whose private half never reached
// disk, and the edge could not sign, so it could not even call Rekey to
// replace the key it does not have. The only remedy would be an operator
// retiring the edge and issuing a fresh setup key, for a crash.
//
// That recovery rests on central answering a repeated Enroll rather than
// refusing it, which is central's property and not this package's. The branch
// that provides it is in the device service's Enroll handler, where it names
// this window.
package identity
