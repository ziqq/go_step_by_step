# 42. Bounded fan-out for ephemeral chat events

[Русская версия](ru/42-ephemeral-fanout.md) · [Runnable example](../lessons/42-ephemeral-fanout/main.go)

One chat event can have several subscribers, including multiple devices. Each needs its own queue. In the reference chat backend, the realtime hub attempts delivery to each selected session and can skip a slow consumer for an ephemeral frame. The example isolates that policy using conversation keys and a string event. Assume the caller has already checked who may join a conversation.

## Run the example

```sh
go run ./lessons/42-ephemeral-fanout
go test -count=1 -v ./lessons/42-ephemeral-fanout
go test -race -count=20 ./lessons/42-ephemeral-fanout
go vet ./lessons/42-ephemeral-fanout
```

```text
fast: typing started
fast: typing stopped
slow: typing started
```

Both queues have capacity one. The fast subscriber reads the first event before the second publish. The slow subscriber's buffer still holds the first event, so it misses the second. Try reading the slow queue between the two publishes to see it receive both.

## Follow a publish

1. The hub creates a buffered channel per subscription. It gives the caller a receive-only view and an unsubscribe function. The caller owns the subscription lifetime and invokes unsubscribe when the session ends.

2. The hub attempts one send per current subscriber of the selected conversation. A send with a `default` branch skips a full queue immediately. Pending events keep their order and other subscribers still get their own delivery attempt. This bounds backlog per subscription. A deployment also needs a session admission limit, like the one in lesson 41.

3. Publish and unsubscribe use the same mutex. Unsubscribe removes the exact queue and closes it under that mutex. This serializes close with every possible send and makes repeated unsubscribe calls safe. Buffered events already enqueued can still be read after close.

4. The mutex protects a short traversal with nonblocking sends. Socket writes belong in the session's consumer. The example's drop policy fits hints that clients can expire, such as typing state. A durable message needs a delivery and recovery contract, such as replay from stored history.

## Practice · Report the result of one publish

Add a `Delivery` result with integer fields `Enqueued` and `Dropped`, and return it from `publish`. Keep the existing queue, scope, and unsubscribe behavior.

1. Count one enqueue per successful send and one drop per full subscriber queue. The sum equals the number of subscribers visited by that call. A successful send only confirms the event entered the channel. An empty or unknown conversation returns two zeros.

2. Test a full subscriber beside a ready subscriber, then free the full queue and publish again. Check both counts and delivered events on each call. Check zero counts after the last unsubscribe. Subscriptions in other conversations must contribute zero.

3. Test publish racing with unsubscribe using a start channel and joined goroutines. With a single initially empty queue, the result must describe one enqueue if publish wins or zero attempts if unsubscribe wins. Verify that reading the closed queue agrees with the reported count. Explain why these counts describe this call even when a subscriber exits immediately afterward.

## Common mistakes

- Sharing one channel between consumers divides events among them. A fan-out needs a separate queue for each subscriber.
- Returning on the first full queue skips some other subscribers.
- Taking a snapshot of channels, unlocking and then sending can race with unsubscribe closing a channel.
- Counting successful sends as client acknowledgments confuses local queuing with network delivery.

## Completion criteria

The demo prints the three lines in order. Existing and added tests pass with the race detector. Results describe only the current publish and agree with the events available to readers, including the full-queue and unsubscribe cases.
