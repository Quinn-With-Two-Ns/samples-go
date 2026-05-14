package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"

	nexusperendpointencryption "github.com/temporalio/samples-go/nexus-per-endpoint-encryption"

	"go.temporal.io/sdk/converter"
)

var portFlag int

func init() {
	flag.IntVar(&portFlag, "port", 8081, "Port to listen on")
}

func main() {
	flag.Parse()

	// A single Codec{} suffices: Decode reads MetadataEncryptionKeyID per
	// payload and looks up the matching key via getKey. The codec server
	// therefore transparently decodes payloads produced under either
	// endpoint's key without per-endpoint dispatch.
	//
	// See ../../encryption/codec-server for a richer codec server example
	// with namespace support, oauth, and UI access.
	handler := converter.NewPayloadCodecHTTPHandler(&nexusperendpointencryption.Codec{}, converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true}))

	srv := &http.Server{
		Addr:    "0.0.0.0:" + strconv.Itoa(portFlag),
		Handler: handler,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	select {
	case <-sigCh:
		_ = srv.Close()
	case err := <-errCh:
		log.Fatal(err)
	}
}
