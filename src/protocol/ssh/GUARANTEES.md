# SSH guarantees

Normative guarantees and test citations for `src/protocol/ssh`.

## Host-key verification has no default

Options MUST specify exactly one of HostKeySHA256 or InsecureIgnoreHostKey, and Dial MUST refuse an invalid configuration before opening a connection or a host-key mismatch on dial.

- WHEN neither HostKeySHA256 nor InsecureIgnoreHostKey is set, or both are set THEN configuration validation fails with an error.
- WHEN HostKeySHA256 does not match the remote host's key fingerprint THEN Dial returns an error refusing the connection.

Proved by: TestHostKeyCallbackRequiresExplicitVerification, TestDialHostKeyMismatchRefused

## A failed wait closes the session

When a Run call fails to complete cleanly due to deadline expiration, context cancellation, or peer connection loss, the Session MUST be closed and subsequent Run calls MUST return ErrSessionClosed.

- WHEN a command wait exceeds its deadline THEN Run returns context.DeadlineExceeded and subsequent Run calls return ErrSessionClosed.
- WHEN a command context is canceled mid-wait THEN Run returns context.Canceled and subsequent Run calls return ErrSessionClosed.
- WHEN the peer closes the connection while a command is running THEN Run returns an error and subsequent Run calls return ErrSessionClosed.

Proved by: TestRunCommandDeadlineExceeded, TestRunCancellationMidWait, TestRunConnectionLostClosesSession

## A command ends at the earliest prompt match

Run MUST end the command at the earliest matching prompt in the accumulated output stream, resolving ties in match position to the earliest prompt in Prompts slice order.

- WHEN multiple prompts match at different positions in output THEN the prompt with the earliest match start offset ends the command.
- WHEN multiple prompts match at the identical start position THEN the prompt appearing earlier in Command.Prompts wins the tie.

Proved by: TestScanPromptEarliestMatchAndTieOrder

## Pagination markers are answered and excluded from output

When Command.MorePattern matches accumulated output, Run MUST write Command.MoreKeystroke and MUST NOT include the marker text in Result.Output.

- WHEN MorePattern matches in the output stream THEN MoreKeystroke is sent to stdin and the matched marker is omitted from Result.Output.

Proved by: TestRunPagination

## Output is capped with the true byte count kept

When command stdout exceeds Command.MaxOutput, Result.Truncated MUST be true and Result.Evidence.BytesReceived MUST record the full un-truncated byte count observed.

- WHEN output exceeds MaxOutput THEN Result.Truncated is true, len(Result.Output) does not exceed MaxOutput, and Evidence.BytesReceived reports the total bytes observed.

Proved by: TestRunOutputCapTruncates

## Stderr saturation does not block stdout completion

Continuous stderr output from the remote shell MUST NOT block stdout prompt detection or command completion.

- WHEN stderr is saturated during command execution THEN Run completes when a prompt appears on stdout.

Proved by: TestRunStderrSaturationDoesNotBlockStdout

## Command redaction replaces sent line in evidence

When Command.Redacted is set, Result.Evidence.Sent MUST equal Command.Redacted rather than Command.Line.

- WHEN Command.Redacted is non-empty THEN Result.Evidence.Sent contains the redacted text without exposing Command.Line.

Proved by: TestRunRedactsSecretFromEvidence
