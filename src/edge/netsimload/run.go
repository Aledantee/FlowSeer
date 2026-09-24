package netsimload

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/stream"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/edge/netsimload/packetio"
	"go.aledante.io/FlowSeer/src/modules/capture/rawsocket"
)

// FlowSource pairs a nonzero flow identity with a fresh finite source. The
// runner advances Source only after the previous frame was sent successfully.
type FlowSource struct {
	ID     fabric.FlowID
	Source stream.Source
}

// Config names the distinct transmit and receive interfaces and the finite
// sources to execute. Drain bounds the receive window after the final send.
type Config struct {
	TXInterface string
	RXInterface string
	Drain       time.Duration
	Flows       []FlowSource
}

// InterfaceInfo is the small interface state needed before opening sockets.
type InterfaceInfo struct {
	Name string
	Up   bool
}

// Clock supplies wall-clock instants and cancellable waits. A production clock
// uses time.Now; tests can advance a monotonic fake clock without sleeping.
type Clock interface {
	Now() time.Time
	Wait(context.Context, time.Time) error
}

// Dependencies are the runner's operating-system and timing seams. A zero
// value selects the Linux packet sender, local raw capture, interface lookup,
// and real clock.
type Dependencies struct {
	Clock            Clock
	ResolveInterface func(string) (InterfaceInfo, error)
	OpenSender       func(string) (packetio.Sender, error)
	OpenReceiver     func(string) (rawsocket.Source, error)
}

// Run executes config using the production packet sender and local-interface
// receiver. It returns the lab observation after successful drain and cleanup.
func Run(ctx context.Context, config Config) (Observation, error) {
	return RunWith(ctx, config, Dependencies{})
}

// RunWith executes config through injected timing, interface, sender, and
// receiver dependencies. Validation and source preflight happen before either
// opener is called.
func RunWith(ctx context.Context, config Config, dependencies Dependencies) (Observation, error) {
	if err := validateConfig(config, dependencies.ResolveInterface); err != nil {
		return Observation{}, err
	}

	if err := preflightSources(config.Flows); err != nil {
		return Observation{}, err
	}

	deps := productionDependencies(dependencies)
	receiver, err := deps.OpenReceiver(config.RXInterface)
	if err != nil {
		return Observation{}, fmt.Errorf("open receive interface %q: %w", config.RXInterface, err)
	}

	sender, err := deps.OpenSender(config.TXInterface)
	if err != nil {
		_ = receiver.Close()
		return Observation{}, fmt.Errorf("open transmit interface %q: %w", config.TXInterface, err)
	}

	return execute(ctx, config, deps.Clock, sender, receiver)
}

type sourceHead struct {
	flowID   fabric.FlowID
	source   stream.Source
	sequence uint64
	at       time.Duration
	frame    ethernet.Frame
	ok       bool
}

type receiverOutcome struct {
	err            error
	interfaceDrops uint64
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

func (realClock) Wait(ctx context.Context, deadline time.Time) error {
	delay := time.Until(deadline)
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func productionDependencies(dependencies Dependencies) Dependencies {
	if dependencies.Clock == nil {
		dependencies.Clock = realClock{}
	}
	if dependencies.ResolveInterface == nil {
		dependencies.ResolveInterface = resolveInterface
	}
	if dependencies.OpenSender == nil {
		dependencies.OpenSender = packetio.OpenSender
	}
	if dependencies.OpenReceiver == nil {
		dependencies.OpenReceiver = func(interfaceName string) (rawsocket.Source, error) {
			return rawsocket.OpenLocalInterface(interfaceName, false, nil)
		}
	}

	return dependencies
}

func resolveInterface(name string) (InterfaceInfo, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return InterfaceInfo{}, err
	}

	return InterfaceInfo{Name: iface.Name, Up: iface.Flags&net.FlagUp != 0}, nil
}

func validateConfig(config Config, resolve func(string) (InterfaceInfo, error)) error {
	if config.TXInterface == "" || config.RXInterface == "" {
		return errors.New("transmit and receive interfaces are required")
	}
	if config.TXInterface == config.RXInterface {
		return fmt.Errorf("transmit and receive interfaces must differ: %q", config.TXInterface)
	}
	if config.Drain < 0 {
		return fmt.Errorf("drain duration must be nonnegative")
	}
	if len(config.Flows) == 0 {
		return errors.New("at least one flow is required")
	}

	if resolve == nil {
		resolve = resolveInterface
	}
	for _, interfaceName := range []string{config.TXInterface, config.RXInterface} {
		info, err := resolve(interfaceName)
		if err != nil {
			return fmt.Errorf("resolve interface %q: %w", interfaceName, err)
		}
		if !info.Up {
			return fmt.Errorf("interface %q is down", interfaceName)
		}
	}

	seen := make(map[fabric.FlowID]struct{}, len(config.Flows))
	for _, flow := range config.Flows {
		if flow.ID == 0 {
			return errors.New("flow ID must be nonzero")
		}
		if _, exists := seen[flow.ID]; exists {
			return fmt.Errorf("flow ID %d is duplicated", flow.ID)
		}
		seen[flow.ID] = struct{}{}
		if flow.Source == nil {
			return fmt.Errorf("flow %d has no source", flow.ID)
		}
	}

	return nil
}

func preflightSources(flows []FlowSource) error {
	for _, flow := range flows {
		clone := flow.Source.Clone()
		if clone == nil {
			return fmt.Errorf("flow %d source clone is nil", flow.ID)
		}
		for {
			_, frame, ok := clone.Next()
			if !ok {
				break
			}
			if len(frame.Payload) < SignatureSize {
				return fmt.Errorf("flow %d yielded a payload of %d octets; need at least %d for the signature", flow.ID, len(frame.Payload), SignatureSize)
			}
			if _, err := frame.Encode(); err != nil {
				return fmt.Errorf("preflight flow %d frame: %w", flow.ID, err)
			}
		}
	}

	return nil
}

func execute(ctx context.Context, config Config, clock Clock, sender packetio.Sender, receiver rawsocket.Source) (observation Observation, runErr error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	accumulator := NewAccumulator(flowIDs(config.Flows)...)
	var observationMu sync.Mutex
	receiveErrors := make(chan error, 1)
	receiverDone := make(chan receiverOutcome, 1)
	var finishOnce sync.Once
	finishReceiver := func(outcome receiverOutcome) {
		finishOnce.Do(func() {
			receiverDone <- outcome
		})
	}
	signalReceiverError := func(err error) {
		if err == nil || errors.Is(err, context.Canceled) {
			return
		}
		select {
		case receiveErrors <- err:
		default:
		}
		cancel()
	}

	frames := receiver.Receive(runCtx)
	spawn.Go(runCtx, "netsimload capture receiver", func() {
		var receiveErr error
		for {
			select {
			case frame, ok := <-frames:
				if !ok {
					if runCtx.Err() == nil {
						receiveErr = errors.New("capture receiver closed before the run completed")
						signalReceiverError(receiveErr)
					}
					goto done
				}
				if frame.Err != nil {
					receiveErr = frame.Err
					signalReceiverError(receiveErr)
					goto done
				}
				withAccumulator(&observationMu, func() { accumulator.RecordFrame(frame) })
			case <-runCtx.Done():
				goto done
			}
		}

	done:
		_, drops, statsErr := receiver.Stats()
		if statsErr != nil && receiveErr == nil {
			receiveErr = fmt.Errorf("read capture statistics: %w", statsErr)
			signalReceiverError(receiveErr)
		}
		finishReceiver(receiverOutcome{err: receiveErr, interfaceDrops: drops})
	}, spawn.ReportTo(func(err error) {
		signalReceiverError(err)
		finishReceiver(receiverOutcome{err: err})
	}))

	heads := make([]sourceHead, len(config.Flows))
	for i, flow := range config.Flows {
		heads[i] = sourceHead{flowID: flow.ID, source: flow.Source}
		advanceHead(&heads[i])
	}
	epoch := clock.Now()

	for {
		index := nextHead(heads)
		if index < 0 {
			break
		}
		if err := receiverError(receiveErrors); err != nil {
			runErr = err
			break
		}

		head := &heads[index]
		if err := clock.Wait(runCtx, epoch.Add(head.at)); err != nil {
			runErr = firstRunError(ctx, err, receiveErrors)
			break
		}
		if err := receiverError(receiveErrors); err != nil {
			runErr = err
			break
		}

		submittedAt := clock.Now()
		signed, err := Sign(head.frame, head.flowID, head.sequence, submittedAt)
		if err != nil {
			runErr = err
			break
		}
		wire, err := signed.Encode()
		if err != nil {
			runErr = fmt.Errorf("encode flow %d frame %d: %w", head.flowID, head.sequence, err)
			break
		}
		if err := sender.Send(runCtx, wire); err != nil {
			runErr = firstRunError(ctx, err, receiveErrors)
			break
		}

		withAccumulator(&observationMu, func() { accumulator.RecordSend(head.flowID, head.sequence) })
		head.sequence++
		advanceHead(head)
	}

	if runErr == nil {
		if err := clock.Wait(runCtx, clock.Now().Add(config.Drain)); err != nil {
			runErr = firstRunError(ctx, err, receiveErrors)
		}
	}

	withAccumulator(&observationMu, func() { accumulator.Close() })
	cancel()
	outcome := <-receiverDone
	closeErr := receiver.Close()
	senderErr := sender.Close()

	if runErr == nil {
		runErr = receiverError(receiveErrors)
	}
	if runErr == nil && outcome.err != nil && !errors.Is(outcome.err, context.Canceled) {
		runErr = outcome.err
	}
	if runErr == nil && closeErr != nil {
		runErr = fmt.Errorf("close receive interface: %w", closeErr)
	}
	if runErr == nil && senderErr != nil {
		runErr = fmt.Errorf("close transmit interface: %w", senderErr)
	}

	withAccumulator(&observationMu, func() {
		accumulator.SetInterfaceDrops(outcome.interfaceDrops)
		observation = accumulator.Snapshot()
	})
	return observation, runErr
}

func withAccumulator(mu *sync.Mutex, record func()) {
	mu.Lock()
	defer mu.Unlock()
	record()
}

func flowIDs(flows []FlowSource) []fabric.FlowID {
	ids := make([]fabric.FlowID, 0, len(flows))
	for _, flow := range flows {
		ids = append(ids, flow.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func advanceHead(head *sourceHead) {
	head.at, head.frame, head.ok = head.source.Next()
}

func nextHead(heads []sourceHead) int {
	index := -1
	for i, head := range heads {
		if !head.ok {
			continue
		}
		if index < 0 || head.at < heads[index].at || (head.at == heads[index].at && head.flowID < heads[index].flowID) {
			index = i
		}
	}
	return index
}

func receiverError(errorsCh <-chan error) error {
	select {
	case err := <-errorsCh:
		return err
	default:
		return nil
	}
}

func firstRunError(parent context.Context, waitErr error, errorsCh <-chan error) error {
	if err := receiverError(errorsCh); err != nil {
		return err
	}
	if err := parent.Err(); err != nil {
		return err
	}
	return waitErr
}
