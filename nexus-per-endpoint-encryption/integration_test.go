package nexusperendpointencryption_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/suite"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	operatorservice "go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
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
// runs the caller workflow once per endpoint, and asserts that each run's
// histories carry the corresponding keyID on every encrypted payload (and
// never carry the other endpoint's keyID).
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

	// Create one Nexus endpoint per configured keyID, all targeting the
	// handler worker's task queue.
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

	ix := &nexusperendpointencryption.Interceptor{
		EndpointKeys: nexusperendpointencryption.EndpointKeys,
	}
	s.encClient, err = client.Dial(client.Options{
		HostPort:  srv.FrontendHostPort(),
		Namespace: testNamespace,
		DataConverter: nexusperendpointencryption.NewEncryptingDataConverter(
			converter.GetDefaultDataConverter(),
			nexusperendpointencryption.DataConverterOptions{},
		),
		Interceptors: []interceptor.ClientInterceptor{ix},
	})
	s.Require().NoError(err)

	s.handlerWorker = worker.New(s.encClient, testHandlerTaskQueue, worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{ix},
	})
	svc := nexus.NewService(service.HelloServiceName)
	s.Require().NoError(svc.Register(handler.EchoOperation, handler.HelloOperation))
	s.handlerWorker.RegisterNexusService(svc)
	s.handlerWorker.RegisterWorkflow(handler.HelloHandlerWorkflow)
	s.handlerWorker.RegisterWorkflow(testableHandlerWorkflow)
	s.Require().NoError(s.handlerWorker.Start())

	s.callerWorker = worker.New(s.encClient, testCallerTaskQueue, worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{ix},
	})
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

// TestHistoryEncryptedPerEndpoint runs the caller workflow once per endpoint
// and asserts each endpoint's caller and handler histories carry only that
// endpoint's keyID -- never the other's. The byte scan over the raw history
// proto catches the keyID inside the payload metadata regardless of which
// event type embeds the payload.
func (s *PerEndpointEncryptionSuite) TestHistoryEncryptedPerEndpoint() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	for endpoint, keyID := range nexusperendpointencryption.EndpointKeys {
		s.Run(endpoint, func() {
			otherKey := s.otherKeyID(keyID)

			callerWFID := "test-caller-" + endpoint + "-" + uuid.NewString()
			wr, err := s.encClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
				ID:        callerWFID,
				TaskQueue: testCallerTaskQueue,
			}, caller.HelloCallerWorkflow, endpoint, "Tester")
			s.Require().NoError(err)

			var result string
			s.Require().NoError(wr.Get(ctx, &result))

			handlerWFID := s.findHandlerWorkflowID(ctx, wr.GetID(), wr.GetRunID())
			s.Require().NotEmpty(handlerWFID, "could not extract handler workflow ID from caller history")

			s.assertHistoryUsesOnly(ctx, "caller", wr.GetID(), wr.GetRunID(), keyID, otherKey)
			s.assertHistoryUsesOnly(ctx, "handler", handlerWFID, "", keyID, otherKey)
		})
	}
}

// otherKeyID returns the keyID for some other endpoint in EndpointKeys.
// Used to confirm that the wrong key does not appear in this endpoint's
// histories.
func (s *PerEndpointEncryptionSuite) otherKeyID(want string) string {
	for _, k := range nexusperendpointencryption.EndpointKeys {
		if k != want {
			return k
		}
	}
	s.FailNow("EndpointKeys must contain at least two distinct keyIDs to make this test meaningful")
	return ""
}

// assertHistoryUsesOnly walks the named workflow's history with the plain
// (non-encrypting) client so payloads stay in ciphertext form, then asserts
// the wanted keyID appears at least once and the unwanted keyID never does.
func (s *PerEndpointEncryptionSuite) assertHistoryUsesOnly(ctx context.Context, label, workflowID, runID, want, unwanted string) {
	wantBytes := []byte(want)
	unwantedBytes := []byte(unwanted)

	iter := s.plainClient.GetWorkflowHistory(ctx, workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	sawWant := false
	for iter.HasNext() {
		event, err := iter.Next()
		s.Require().NoError(err)
		b, err := proto.Marshal(event)
		s.Require().NoError(err)
		if bytes.Contains(b, wantBytes) {
			sawWant = true
		}
		s.Require().False(bytes.Contains(b, unwantedBytes),
			"%s history event %d (%s) contains unwanted keyID %q",
			label, event.GetEventId(), event.GetEventType(), unwanted)
	}
	s.Require().Truef(sawWant, "%s history never contains expected keyID %q (workflow %s)", label, want, workflowID)
}

// --- Test-only workflow exercising signal, query, and update -------------
//
// testableHandlerWorkflow runs on the same handler worker as the production
// sample's HelloHandlerWorkflow. The test starts it directly via the
// encrypting client with CryptContext seeded on the Go ctx; the
// ContextPropagator then ferries CryptContext onto the workflow's start
// header so the workflow inbound interceptor sees an endpoint when wrapping
// query and update results in EndpointAware.

type testableUpdateInput struct {
	NewName string
}

type testableUpdateOutput struct {
	Previous string
	Current  string
}

type testableSignal struct {
	Note string
}

type testableOutput struct {
	Final string
}

const (
	testableQueryCurrentName = "current-name"
	testableUpdateRename     = "rename"
	testableSignalComplete   = "complete"
)

func testableHandlerWorkflow(ctx workflow.Context, name string) (testableOutput, error) {
	current := name

	if err := workflow.SetQueryHandler(ctx, testableQueryCurrentName, func() (string, error) {
		return current, nil
	}); err != nil {
		return testableOutput{}, err
	}

	if err := workflow.SetUpdateHandler(ctx, testableUpdateRename, func(ctx workflow.Context, in testableUpdateInput) (testableUpdateOutput, error) {
		prev := current
		current = in.NewName
		return testableUpdateOutput{Previous: prev, Current: current}, nil
	}); err != nil {
		return testableOutput{}, err
	}

	var sig testableSignal
	workflow.GetSignalChannel(ctx, testableSignalComplete).Receive(ctx, &sig)
	return testableOutput{Final: current + ":" + sig.Note}, nil
}

// TestSignalQueryUpdateEncryption verifies that signal, query, and update
// code paths produce histories whose encrypted payloads are tagged with the
// configured endpoint's keyID. CryptContext is seeded on the Go ctx so the
// existing ContextPropagator carries it onto the workflow's start header --
// no Nexus boundary involved.
func (s *PerEndpointEncryptionSuite) TestSignalQueryUpdateEncryption() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	for endpoint, keyID := range nexusperendpointencryption.EndpointKeys {
		s.Run(endpoint, func() {
			otherKey := s.otherKeyID(keyID)

			cryptCtx := context.WithValue(ctx, nexusperendpointencryption.PropagateKey,
				nexusperendpointencryption.CryptContext{KeyID: keyID, Endpoint: endpoint})

			wfID := "test-sqe-" + endpoint + "-" + uuid.NewString()
			wr, err := s.encClient.ExecuteWorkflow(cryptCtx, client.StartWorkflowOptions{
				ID:        wfID,
				TaskQueue: testHandlerTaskQueue,
			}, testableHandlerWorkflow, "Alice")
			s.Require().NoError(err)

			// Query: result flows through workflow inbound interceptor
			// -> EndpointAware wrap -> data converter ToPayloads override.
			qr, err := s.encClient.QueryWorkflow(cryptCtx, wfID, wr.GetRunID(), testableQueryCurrentName)
			s.Require().NoError(err)
			var qResult string
			s.Require().NoError(qr.Get(&qResult))
			s.Require().Equal("Alice", qResult)

			// Update: same wrap path; result also lands in workflow history.
			uh, err := s.encClient.UpdateWorkflow(cryptCtx, client.UpdateWorkflowOptions{
				WorkflowID:   wfID,
				RunID:        wr.GetRunID(),
				UpdateName:   testableUpdateRename,
				Args:         []any{testableUpdateInput{NewName: "Bob"}},
				WaitForStage: client.WorkflowUpdateStageCompleted,
			})
			s.Require().NoError(err)
			var uResult testableUpdateOutput
			s.Require().NoError(uh.Get(cryptCtx, &uResult))
			s.Require().Equal("Alice", uResult.Previous)
			s.Require().Equal("Bob", uResult.Current)

			// Signal release; verifies the signal path also encodes
			// through the per-endpoint codec for the workflow at rest.
			s.Require().NoError(s.encClient.SignalWorkflow(cryptCtx, wfID, wr.GetRunID(), testableSignalComplete, testableSignal{Note: "done"}))

			var final testableOutput
			s.Require().NoError(wr.Get(cryptCtx, &final))
			s.Require().Equal("Bob:done", final.Final)

			s.assertHistoryUsesOnly(ctx, "testable-handler", wfID, wr.GetRunID(), keyID, otherKey)
		})
	}
}

// findHandlerWorkflowID pulls the workflow-run operation token out of the
// caller workflow's NexusOperationStarted event and decodes it to recover
// the handler workflow's ID.
func (s *PerEndpointEncryptionSuite) findHandlerWorkflowID(ctx context.Context, callerID, callerRunID string) string {
	iter := s.plainClient.GetWorkflowHistory(ctx, callerID, callerRunID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		s.Require().NoError(err)
		started, ok := event.Attributes.(*historypb.HistoryEvent_NexusOperationStartedEventAttributes)
		if !ok {
			continue
		}
		token := started.NexusOperationStartedEventAttributes.GetOperationToken()
		if token == "" {
			continue
		}
		raw, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(token)
		s.Require().NoError(err)
		var decoded struct {
			WorkflowID string `json:"wid"`
		}
		s.Require().NoError(json.Unmarshal(raw, &decoded))
		if decoded.WorkflowID != "" {
			return decoded.WorkflowID
		}
	}
	return ""
}
