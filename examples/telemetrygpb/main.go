/*
gRPC Client
*/

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"time"

	proto "github.com/golang/protobuf/proto"
	xr "github.com/nleiva/xrgrpc"
	"github.com/nleiva/xrgrpc/proto/telemetry"
	lldp "github.com/nleiva/xrgrpc/proto/telemetry/lldp"
	"github.com/pkg/errors"
)

func prettyprint(b []byte) ([]byte, error) {
	var out bytes.Buffer
	err := json.Indent(&out, b, "", "  ")
	return out.Bytes(), err
}

func main() {
	// Subs options; LLDP, we will add some more
	p := flag.String("subs", "LLDP", "Telemetry Subscription")
	// Encoding option; defaults to GPB (only one supported in this example)
	enc := flag.String("enc", "gpb", "Encoding: 'json', 'gpb' or 'gpbkv'")
	// Config file; defaults to "config.json"
	cfg := flag.String("cfg", "../input/config.json", "Configuration file")

	flag.Parse()
	mape := map[string]int64{
		"gpb":   2,
		"gpbkv": 3,
		"json":  4,
	}
	e := mape[*enc]
	if e == 0 {
		log.Fatalf("Encoding option '%v' not supported", *enc)
	}

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	id := r.Int63n(1000)

	// Define target parameters from the configuration file
	targets := xr.NewDevices()
	err := xr.DecodeJSONConfig(targets, *cfg)
	if err != nil {
		log.Fatalf("Could not read the config: %v\n", err)
	}

	// Setup a connection to the target. 'd' is the index of the router
	// in the config file.
	d := 0
	// Adjust timeout to increase gRPC session lifespan to be able to receive
	// Streaming Telemetry data for a period of time.
	targets.Routers[d].Timeout = 20
	conn, ctx, err := xr.Connect(targets.Routers[d])
	if err != nil {
		log.Fatalf("Could not setup a client connection to %s, %v", targets.Routers[d].Host, err)
	}
	defer conn.Close()

	ch, err := xr.GetSubscription(ctx, conn, *p, id, e)
	if err != nil {
		log.Fatalf("Could not setup Telemetry Subscription: %v\n", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	c := make(chan os.Signal, 1)
	// If no signals are provided, all incoming signals will be relayed to c.
	// Otherwise, just the provided signals will. E.g.: signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	signal.Notify(c, os.Interrupt)
	defer func() {
		signal.Stop(c)
		cancel()
	}()
	go func() {
		select {
		case <-c:
			fmt.Printf("\nManually cancelled the session to %v\n\n", targets.Routers[d].Host)
			cancel()
		case <-ctx.Done():
			fmt.Printf("\ngRPC session timed out after %v seconds\n\n", targets.Routers[d].Timeout)
		}
		// panic("Show me the stack")
		os.Exit(0)
	}()

	for {
		tele := <-ch
		message := new(telemetry.Telemetry)
		err := proto.Unmarshal(tele, message)
		if err != nil {
			log.Fatalf("Could not unmarshall the message: %v\n", err)
		}
		fmt.Printf("Time %v, Path: %v\n", message.GetMsgTimestamp(), message.GetEncodingPath())

		for _, row := range message.GetDataGpb().GetRow() {
			// From GPB we have row.GetTimestamp(), row.GetKeys() and row.GetContent()
			keys := new(lldp.LldpNeighbor_KEYS)
			output, err := decodeKeys(row.GetKeys(), keys)
			if err != nil {
				log.Fatalf("Could decode Keys: %v\n", err)
			}
			fmt.Println(output)
			content := row.GetContent()
			nbrs := new(lldp.LldpNeighbor)
			err = proto.Unmarshal(content, nbrs)

			for _, nei := range nbrs.LldpNeighbor {
				n := nei.GetDetail()
				a := n.GetNetworkAddresses().GetLldpAddrEntry()[0].Address.GetIpv6Address()
				fmt.Printf("Type: %s, Address %s \n\n", n.GetSystemDescription(), a)
			}
		}
	}
}

func decodeKeys(bk []byte, k *lldp.LldpNeighbor_KEYS) (string, error) {
	err := proto.Unmarshal(bk, k)
	s := ""
	if err != nil {
		return s, errors.Wrap(err, "Could not unmarshall the message keys")
	}
	b, err := json.Marshal(k)
	if err != nil {
		return s, errors.Wrap(err, "Could not marshall into JSON")
	}
	b, err = prettyprint(b)
	if err != nil {
		return s, errors.Wrap(err, "Could not pretty-print the message")
	}
	return string(b), err
}
