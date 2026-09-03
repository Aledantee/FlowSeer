package syslog_test

import (
	"context"
	"fmt"
	"time"

	"go.aledante.io/FlowSeer/src/common/syslog"
)

func ExampleParser_Parse() {
	parser, err := syslog.NewParser(syslog.ParseOptions{CaptureRaw: true})
	if err != nil {
		fmt.Println(err)
		return
	}
	received := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	record, err := parser.Parse([]byte("<189>*Mar 1 00:00:01: %LINK-3-UPDOWN: Interface down"), syslog.Observation{ReceivedAt: received})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(record.Vendor.Module.Value, record.Vendor.Mnemonic.Value)
	fmt.Println(record.DeviceTime.Instant == nil, record.Observation.ReceivedAt.Year(), record.Raw != nil)
	// Output:
	// LINK UPDOWN
	// true 2026 true
}

func ExampleReceiver_Next() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	receiver, err := syslog.Listen(ctx, []syslog.ListenConfig{{Transport: syslog.UDP, Address: "127.0.0.1:0"}}, syslog.ReceiverOptions{})
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() {
		if err := receiver.Close(); err != nil {
			fmt.Println(err)
		}
	}()
	sender, err := syslog.NewSender(receiver.Addresses()[0].Address, syslog.UDP, syslog.SenderOptions{})
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() {
		if err := sender.Close(); err != nil {
			fmt.Println(err)
		}
	}()
	report, err := sender.Send(ctx, syslog.Record{Priority: syslog.Text("13"), Content: []byte("hello")}, syslog.EncodeOptions{Format: syslog.RFC5424})
	if err != nil {
		fmt.Println(report.Encoding, err)
		return
	}
	record, err := receiver.Next(ctx)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(record.Content), record.Raw == nil, record.Observation.ReceivedAt.IsZero())
	// Output: hello true false
}
