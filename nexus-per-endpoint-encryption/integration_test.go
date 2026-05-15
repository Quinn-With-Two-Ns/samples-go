package nexusperendpointencryption_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/suite"
	enumspb "go.temporal.io/api/enums/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	operatorservice "go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/proto"

	nexusperendpointencryption "github.com/temporalio/samples-go/nexus-per-endpoint-encryption"
	"github.com/temporalio/samples-go/nexus-per-endpoint-encryption/caller"
	"github.com/temporalio/samples-go/nexus-per-endpoint-encryption/handler"
	"github.com/temporalio/samples-go/nexus-per-endpoint-encryption/service"
)

const (
	testNamespace        = "default"
	testHandlerTaskQueue = "nexus-per-endpoint-encryption-handler-test-tq"
	testCallerTaskQueue  = "nexus-per-endpoint-encryption-caller-test-tq"
)

// PerEndpointEncryptionSuite spins up a real Temporal dev server, registers
// the sample's caller and handler workers (one Nexus endpoint per keyID),
// runs the caller workflow once (which calls four Nexus operations -- two per
// endpoint -- in a single run), and asserts both endpoints' keyIDs appear in
// the caller's history payloads.
type PerEndpointEncryptionSuite struct {
	suite.Suite
	server        *testsuite.DevServer
	plainClient   client.Client
	encClient     client.Client
	handlerWorker worker.Worker
	callerWorker  worker.Worker
}

func TestPerEndpointEncryptionSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("dev-server integration test; skipped in -short")
	}
	suite.Run(t, new(PerEndpointEncryptionSuite))
}

func (s *PerEndpointEncryptionSuite) SetupSuite() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	srv, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{
		LogLevel: "error",
		ClientOptions: &client.Options{
			Namespace: testNamespace,
		},
	})
	s.Require().NoError(err)
	s.server = srv
	s.plainClient = srv.Client()

	for endpoint := range nexusperendpointencryption.EndpointKeys {
		_, err := s.plainClient.OperatorService().CreateNexusEndpoint(ctx, &operatorservice.CreateNexusEndpointRequest{
			Spec: &nexuspb.EndpointSpec{
				Name: endpoint,
				Target: &nexuspb.EndpointTarget{
					Variant: &nexuspb.EndpointTarget_Worker_{
						Worker: &nexuspb.EndpointTarget_Worker{
							Namespace: testNamespace,
							TaskQueue: testHandlerTaskQueue,
						},
					},
				},
			},
		})
		s.Require().NoError(err)
	}

	s.encClient, err = client.Dial(client.Options{
		HostPort:  srv.FrontendHostPort(),
		Namespace: testNamespace,
		DataConverter: converter.NewCodecDataConverter(
			converter.GetDefaultDataConverter(),
			&nexusperendpointencryption.Codec{},
		),
	})
	s.Require().NoError(err)

	s.handlerWorker = worker.New(s.encClient, testHandlerTaskQueue, worker.Options{})
	svc := nexus.NewService(service.HelloServiceName)
	s.Require().NoError(svc.Register(handler.EchoOperation, handler.HelloOperation))
	s.handlerWorker.RegisterNexusService(svc)
	s.handlerWorker.RegisterWorkflow(handler.HelloHandlerWorkflow)
	s.Require().NoError(s.handlerWorker.Start())

	s.callerWorker = worker.New(s.encClient, testCallerTaskQueue, worker.Options{})
	s.callerWorker.RegisterWorkflow(caller.HelloCallerWorkflow)
	s.Require().NoError(s.callerWorker.Start())
}

func (s *PerEndpointEncryptionSuite) TearDownSuite() {
	if s.callerWorker != nil {
		s.callerWorker.Stop()
	}
	if s.handlerWorker != nil {
		s.handlerWorker.Stop()
	}
	if s.encClient != nil {
		s.encClient.Close()
	}
	if s.plainClient != nil {
		s.plainClient.Close()
	}
	if s.server != nil {
		_ = s.server.Stop()
	}
}

// TestHistoryEncryptedPerEndpoint runs the caller workflow once. The workflow
// invokes echo + hello on each of endpoint-a and endpoint-b, so its history
// must carry BOTH keyIDs. The byte scan over the raw history proto catches
// each keyID inside payload metadata regardless of event type.
func (s *PerEndpointEncryptionSuite) TestHistoryEncryptedPerEndpoint() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	callerWFID := "test-caller-" + uuid.NewString()
	wr, err := s.encClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        callerWFID,
		TaskQueue: testCallerTaskQueue,
	}, caller.HelloCallerWorkflow, "Tester")
	s.Require().NoError(err)

	var result string
	s.Require().NoError(wr.Get(ctx, &result))

	wantKeys := make([]string, 0, len(nexusperendpointencryption.EndpointKeys))
	for _, k := range nexusperendpointencryption.EndpointKeys {
		wantKeys = append(wantKeys, k)
	}
	s.assertHistoryContainsAll(ctx, "caller", wr.GetID(), wr.GetRunID(), wantKeys)
}

// assertHistoryContainsAll walks the named workflow's history with the plain
// (non-encrypting) client so payloads stay in ciphertext form, then asserts
// every expected keyID appears at least once.
func (s *PerEndpointEncryptionSuite) assertHistoryContainsAll(ctx context.Context, label, workflowID, runID string, wantKeys []string) {
	seen := make(map[string]bool, len(wantKeys))
	for _, k := range wantKeys {
		seen[k] = false
	}

	iter := s.plainClient.GetWorkflowHistory(ctx, workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		s.Require().NoError(err)
		b, err := proto.Marshal(event)
		s.Require().NoError(err)
		for k := range seen {
			if !seen[k] && bytes.Contains(b, []byte(k)) {
				seen[k] = true
			}
		}
	}
	for k, ok := range seen {
		s.Require().Truef(ok, "%s history never contains expected keyID %q (workflow %s)", label, k, workflowID)
	}
}
