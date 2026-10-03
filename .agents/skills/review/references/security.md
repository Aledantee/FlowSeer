# Reviewing a change that reads untrusted input

Load this when the change handles input from an author step 3 names as
untrusted: a network peer, a device, or a runtime user. It sets what a
security finding must show and where to look.

## What a finding must show

A security finding names all six. A concern missing one is a note under
hardening, since a missing best practice with nobody affected is not a
defect of this change.

| Part | Question |
| --- | --- |
| Principal | Who is the lower-trust party, and what can it already do? |
| Input | Which value, action, or selector does the code accept from it? |
| Control | Which check should reject, bind, limit, or scope that input? |
| Path | Which lines run after that check, or instead of it? |
| Boundary | Whose data, device, or process does the path then reach? |
| Result | What does the affected party observe: a wrong return, a foreign record, a stopped service? |

State the result at the strength shown. A decode error is not a crash, and
a crash is not code execution.

When the deciding fact lives outside the repository (a proxy setting, a
broker ACL, an identity provider's policy, how a deployment is wired),
neither assume it nor dismiss it. Report the finding as "needs validation"
with the exact missing fact and who can check it, and give it no severity.
Step 4's "unverified" is for a claim about this tree that you could not
confirm. "Needs validation" is for a fact the tree does not hold.

## Where to look

Read the sibling paths to the same effect, since a control present on one
is often absent on the next: the batch form, the retry, the cancellation,
the error return, the streaming variant of a unary call.

Service and RPC surfaces:

- An interceptor that authenticates or authorizes is registered for every
  method, streaming ones included, and a long stream checks again where the
  scope can change.
- The identity the channel proved is the identity the handler acts for. A
  field the caller sets must not select the principal or the tenant.
- Schema validation proves a message's shape. It says nothing about who
  sent it or whether the sender may name that resource.

Isolation:

- A tenant or owner field on a record is not isolation. Find the query,
  key, or subject that enforces the scope on each read and each write.
- A cache key, bucket, bus subject, or deduplication key carries the same
  scope as the record it stands for.
- A derived copy (an index, an export, a log line, a trace attribute) keeps
  the scope and the redaction of its origin.

Device and peer input:

- A length, count, or offset taken from the wire is bounded before it sizes
  an allocation, a loop, or a slice.
- Two decoders of one message (a generated one and a hand-written fast
  path, two schema versions) agree on every field a decision rests on.
- A value read from a device is data when it reaches a command line, a
  prompt pattern, or a log.
