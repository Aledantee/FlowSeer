// Package subscribeloop holds a central stream open across reconnects: the
// backoff between attempts, the Contact counters a watcher reads from
// outside the loop, the resync that runs before every attempt, and the
// receive loop that classifies what leaves it. It is generic over the
// streamed message type so a second caller, with its own stream and its own
// event names, shares this machinery rather than hand-writing a second copy
// of it.
//
// The loop asks nothing of the message type beyond Receive/Msg/Err/Close,
// which *connect.ServerStreamForClient[T] already satisfies, and everything
// else through Config: an Opener that starts one attempt, a Handler that
// applies one message, and an Events naming what gets logged. Two of the
// values logged are the loop's own counts rather than anything a per-message
// hook could derive — the connection count on the connected event and the
// delivered count on the disconnected event — so Events carries the
// attribute keys they are logged under and the loop supplies the values.
package subscribeloop
