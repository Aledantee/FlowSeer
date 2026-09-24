// Package main provides the netsimload transmit and comparison commands.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/stream"
	"go.aledante.io/FlowSeer/src/edge/netsimload"
)

const inputVersion = 1

type transmitDocument struct {
	Version     int            `json:"version"`
	TXInterface string         `json:"tx_interface"`
	RXInterface string         `json:"rx_interface"`
	Drain       string         `json:"drain"`
	Flows       []transmitFlow `json:"flows"`
}

type transmitFlow struct {
	ID              uint32  `json:"id"`
	FrameHex        string  `json:"frame_hex"`
	FramesPerSecond *uint64 `json:"frames_per_second"`
	BitsPerSecond   *uint64 `json:"bits_per_second"`
	Count           *int    `json:"count"`
	Duration        *string `json:"duration"`
	Burst           int     `json:"burst"`
	Gap             string  `json:"gap"`
	Start           string  `json:"start"`
	Seed            uint64  `json:"seed"`
}

type compareDocument struct {
	Version     int                        `json:"version"`
	Destination string                     `json:"destination"`
	Simulator   netsimload.SimulatorReport `json:"simulator"`
	Lab         netsimload.Observation     `json:"lab"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, netsimload.Dependencies{}))
}

func run(args []string, input io.Reader, output, diagnostics io.Writer, dependencies netsimload.Dependencies) int {
	if len(args) != 1 {
		fmt.Fprintln(diagnostics, "usage: netsimload transmit|compare")
		return 2
	}

	var err error
	switch args[0] {
	case "transmit":
		err = transmit(input, output, dependencies)
	case "compare":
		err = compare(input, output)
	default:
		fmt.Fprintf(diagnostics, "unknown command %q\n", args[0])
		return 2
	}
	if err != nil {
		fmt.Fprintln(diagnostics, err)
		return 1
	}

	return 0
}

func transmit(input io.Reader, output io.Writer, dependencies netsimload.Dependencies) error {
	var document transmitDocument
	if err := decodeJSON(input, &document); err != nil {
		return err
	}
	if document.Version != inputVersion {
		return fmt.Errorf("unsupported transmit document version %d", document.Version)
	}
	drain, err := parseDuration("drain", document.Drain)
	if err != nil {
		return err
	}

	flows := make([]netsimload.FlowSource, 0, len(document.Flows))
	for _, inputFlow := range document.Flows {
		spec, err := streamSpec(inputFlow)
		if err != nil {
			return fmt.Errorf("flow %d: %w", inputFlow.ID, err)
		}
		source, err := spec.Source()
		if err != nil {
			return fmt.Errorf("flow %d: %w", inputFlow.ID, err)
		}
		flows = append(flows, netsimload.FlowSource{ID: fabric.FlowID(inputFlow.ID), Source: source})
	}

	observation, err := netsimload.RunWith(context.Background(), netsimload.Config{
		TXInterface: document.TXInterface,
		RXInterface: document.RXInterface,
		Drain:       drain,
		Flows:       flows,
	}, dependencies)
	if err != nil {
		return err
	}

	return writeJSON(output, observation)
}

func compare(input io.Reader, output io.Writer) error {
	var document compareDocument
	if err := decodeJSON(input, &document); err != nil {
		return err
	}
	if document.Version != inputVersion {
		return fmt.Errorf("unsupported compare document version %d", document.Version)
	}
	if document.Destination == "" {
		return errors.New("compare destination is required")
	}

	flowIDs := make(map[fabric.FlowID]struct{}, len(document.Simulator.Flows))
	for _, flow := range document.Simulator.Flows {
		if flow.ID == 0 {
			return errors.New("simulator flow ID must be nonzero")
		}
		if _, exists := flowIDs[flow.ID]; exists {
			return fmt.Errorf("simulator flow ID %d is duplicated", flow.ID)
		}
		flowIDs[flow.ID] = struct{}{}
	}
	for id := range document.Lab.Flows {
		if _, exists := flowIDs[id]; !exists {
			return fmt.Errorf("lab flow ID %d is unknown to the simulator input", id)
		}
	}

	report := netsimload.NewReportFromSimulator(document.Simulator, document.Lab, document.Destination)
	return report.WriteJSON(output)
}

func streamSpec(input transmitFlow) (stream.Spec, error) {
	if (input.FramesPerSecond == nil) == (input.BitsPerSecond == nil) {
		return stream.Spec{}, errors.New("exactly one of frames_per_second or bits_per_second is required")
	}
	if (input.Count == nil) == (input.Duration == nil) {
		return stream.Spec{}, errors.New("exactly one of count or duration is required")
	}
	if input.ID == 0 {
		return stream.Spec{}, errors.New("flow ID must be nonzero")
	}

	wire, err := hex.DecodeString(input.FrameHex)
	if err != nil {
		return stream.Spec{}, fmt.Errorf("decode frame_hex: %w", err)
	}
	frame, err := ethernet.Decode(wire)
	if err != nil {
		return stream.Spec{}, fmt.Errorf("decode frame_hex Ethernet frame: %w", err)
	}
	gap, err := parseDuration("gap", input.Gap)
	if err != nil {
		return stream.Spec{}, err
	}
	start, err := parseDuration("start", input.Start)
	if err != nil {
		return stream.Spec{}, err
	}

	spec := stream.Spec{
		Frame: frame,
		Rate:  stream.Rate{},
		Burst: input.Burst,
		Gap:   gap,
		Start: start,
		Seed:  input.Seed,
	}
	if input.FramesPerSecond != nil {
		spec.Rate.FramesPerSecond = *input.FramesPerSecond
	} else {
		spec.Rate.BitsPerSecond = *input.BitsPerSecond
	}
	if input.Count != nil {
		spec.Count = *input.Count
	} else {
		duration, err := parseDuration("duration", *input.Duration)
		if err != nil {
			return stream.Spec{}, err
		}
		spec.Duration = duration
	}

	return spec, spec.Validate()
}

func parseDuration(name, value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s duration %q: %w", name, value, err)
	}
	if duration < 0 {
		return 0, fmt.Errorf("%s duration must be nonnegative", name)
	}
	return duration, nil
}

func decodeJSON(input io.Reader, destination any) error {
	decoder := json.NewDecoder(input)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("decode JSON: multiple documents are not allowed")
		}
		return fmt.Errorf("decode JSON: %w", err)
	}
	return nil
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
