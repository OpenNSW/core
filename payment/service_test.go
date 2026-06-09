package payment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// --- mocks -----------------------------------------------------------------

// mockRepo is an in-memory PaymentRepository keyed by reference number.
type mockRepo struct {
	txs map[string]*PaymentTransaction

	createErr error
	getErr    error
	updateErr error

	// collide makes the first N GetByReferenceNumber calls report an existing
	// row, used to exercise the reference-collision retry loop.
	collide int

	getCount    int
	updateCount int
}

func newMockRepo() *mockRepo { return &mockRepo{txs: map[string]*PaymentTransaction{}} }

func (m *mockRepo) Create(_ context.Context, tx *PaymentTransaction) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.txs[tx.ReferenceNumber] = tx
	return nil
}

func (m *mockRepo) GetByReferenceNumber(_ context.Context, ref string) (*PaymentTransaction, error) {
	m.getCount++
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.collide > 0 {
		m.collide--
		return &PaymentTransaction{ReferenceNumber: ref}, nil
	}
	if tx, ok := m.txs[ref]; ok {
		return tx, nil
	}
	return nil, nil
}

func (m *mockRepo) GetByReferenceNumberForUpdate(ctx context.Context, ref string) (*PaymentTransaction, error) {
	return m.GetByReferenceNumber(ctx, ref)
}

func (m *mockRepo) GetByTaskID(_ context.Context, taskID string) (*PaymentTransaction, error) {
	for _, tx := range m.txs {
		if tx.TaskID == taskID {
			return tx, nil
		}
	}
	return nil, nil
}

func (m *mockRepo) Update(_ context.Context, tx *PaymentTransaction) error {
	m.updateCount++
	if m.updateErr != nil {
		return m.updateErr
	}
	m.txs[tx.ReferenceNumber] = tx
	return nil
}

func (m *mockRepo) UpdateStatus(_ context.Context, ref string, status PaymentStatus) error {
	if tx, ok := m.txs[ref]; ok {
		tx.Status = status
	}
	return nil
}

func (m *mockRepo) RunInTransaction(ctx context.Context, fn func(repo PaymentRepository) error) error {
	return fn(m)
}

func (m *mockRepo) WithTx(*gorm.DB) PaymentRepository { return m }

// mockRegistry returns a single configured gateway.
type mockRegistry struct {
	gw     PaymentGateway
	getErr error
	infos  []GatewayInfo
}

func (m *mockRegistry) Get(string) (PaymentGateway, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.gw, nil
}

func (m *mockRegistry) ListInfo() []GatewayInfo { return m.infos }

type completeCall struct {
	taskID  string
	payload map[string]any
}

type mockTaskCompleter struct {
	calls []completeCall
	err   error
}

func (m *mockTaskCompleter) CompleteTaskStep(_ context.Context, taskID string, payload map[string]any) error {
	m.calls = append(m.calls, completeCall{taskID: taskID, payload: payload})
	return m.err
}

func validCheckoutReq() CreateCheckoutRequest {
	return CreateCheckoutRequest{
		GatewayID: "govpay",
		Amount:    decimal.RequireFromString("1500.00"),
		Currency:  "LKR",
		ExpiresAt: time.Now().Add(time.Hour),
		Metadata:  map[string]string{"task_id": "task-1"},
	}
}

// --- CreateCheckoutSession --------------------------------------------------

func TestCreateCheckoutSession_Success(t *testing.T) {
	repo := newMockRepo()
	gw := new(MockGateway)
	// Write-ahead invariant: the PENDING row must exist before the gateway is called.
	gw.On("CreateSession", mock.Anything, mock.Anything).
		Run(func(mock.Arguments) {
			require.Len(t, repo.txs, 1)
			for _, tx := range repo.txs {
				assert.Equal(t, PaymentStatusPending, tx.Status)
			}
		}).
		Return(&SessionResponse{
			SessionID:    "sess-1",
			Type:         FlowTypeInstruction,
			Instructions: "pay now",
		}, nil)

	svc := NewPaymentService(repo, &mockRegistry{gw: gw})

	resp, err := svc.CreateCheckoutSession(context.Background(), validCheckoutReq())
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, strings.HasPrefix(resp.ReferenceNumber, "TNSW"), "ref %q", resp.ReferenceNumber)
	assert.Equal(t, "sess-1", resp.SessionID)
	assert.Equal(t, FlowTypeInstruction, resp.Type)
	assert.Positive(t, resp.ExpiresIn)

	stored := repo.txs[resp.ReferenceNumber]
	require.NotNil(t, stored)
	assert.Equal(t, PaymentStatusPending, stored.Status)
	assert.Equal(t, "sess-1", stored.SessionID)
	assert.Equal(t, "task-1", stored.TaskID)
	gw.AssertExpectations(t)
}

func TestCreateCheckoutSession_ValidationErrors(t *testing.T) {
	cases := map[string]func(*CreateCheckoutRequest){
		"missing task_id": func(r *CreateCheckoutRequest) { r.Metadata = nil },
		"zero amount":     func(r *CreateCheckoutRequest) { r.Amount = decimal.Zero },
		"negative amount": func(r *CreateCheckoutRequest) { r.Amount = decimal.RequireFromString("-5") },
		"empty currency":  func(r *CreateCheckoutRequest) { r.Currency = "" },
		"past expiry":     func(r *CreateCheckoutRequest) { r.ExpiresAt = time.Now().Add(-time.Hour) },
		"empty gateway":   func(r *CreateCheckoutRequest) { r.GatewayID = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			repo := newMockRepo()
			svc := NewPaymentService(repo, &mockRegistry{gw: new(MockGateway)})
			req := validCheckoutReq()
			mutate(&req)

			_, err := svc.CreateCheckoutSession(context.Background(), req)
			require.Error(t, err)
			assert.Empty(t, repo.txs, "nothing should be persisted on a validation failure")
		})
	}
}

func TestCreateCheckoutSession_GatewayNotFound(t *testing.T) {
	repo := newMockRepo()
	svc := NewPaymentService(repo, &mockRegistry{getErr: errors.New("not registered")})

	_, err := svc.CreateCheckoutSession(context.Background(), validCheckoutReq())
	require.Error(t, err)
	assert.Empty(t, repo.txs)
}

func TestCreateCheckoutSession_GatewaySessionError_MarksFailed(t *testing.T) {
	repo := newMockRepo()
	gw := new(MockGateway)
	gw.On("CreateSession", mock.Anything, mock.Anything).Return(nil, errors.New("boom"))
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})

	_, err := svc.CreateCheckoutSession(context.Background(), validCheckoutReq())
	require.Error(t, err)
	require.Len(t, repo.txs, 1)
	for _, tx := range repo.txs {
		assert.Equal(t, PaymentStatusFailed, tx.Status, "row must be marked FAILED, not left PENDING")
	}
}

func TestCreateCheckoutSession_ReferenceCollisionRetry(t *testing.T) {
	repo := newMockRepo()
	repo.collide = 1 // first candidate "exists", second is free
	gw := new(MockGateway)
	gw.On("CreateSession", mock.Anything, mock.Anything).Return(&SessionResponse{}, nil)
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})

	resp, err := svc.CreateCheckoutSession(context.Background(), validCheckoutReq())
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 2, repo.getCount, "should retry once after a collision")
}

func TestCreateCheckoutSession_PersistError(t *testing.T) {
	repo := newMockRepo()
	repo.createErr = errors.New("db down")
	gw := new(MockGateway) // CreateSession must not be reached
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})

	_, err := svc.CreateCheckoutSession(context.Background(), validCheckoutReq())
	require.Error(t, err)
	gw.AssertNotCalled(t, "CreateSession", mock.Anything, mock.Anything)
}

// --- ValidateReference ------------------------------------------------------

func validateGateway(ref string) *MockGateway {
	gw := new(MockGateway)
	gw.On("ExtractReferenceNumber", mock.Anything, mock.Anything).Return(ref, nil)
	return gw
}

func TestValidateReference_PayablePending(t *testing.T) {
	repo := newMockRepo()
	repo.txs["TNSW1"] = &PaymentTransaction{
		ReferenceNumber: "TNSW1",
		GatewayID:       "govpay",
		Status:          PaymentStatusPending,
		Amount:          decimal.RequireFromString("100"),
		Currency:        "LKR",
		ExpiryDate:      time.Now().Add(time.Hour),
	}
	gw := validateGateway("TNSW1")
	gw.On("HandleValidateReference", mock.Anything,
		mock.MatchedBy(func(tx *ValidationTransaction) bool { return tx != nil && tx.ReferenceNumber == "TNSW1" }),
		true, mock.Anything).
		Return(&ValidationResponse{HTTPStatus: 200, Payload: []byte(`{}`)}, nil)
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})

	resp, err := svc.ValidateReference(context.Background(), "govpay", []byte(`{}`))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.HTTPStatus)
	gw.AssertExpectations(t)
}

func TestValidateReference_UnknownReference(t *testing.T) {
	repo := newMockRepo()
	gw := validateGateway("NOPE")
	gw.On("HandleValidateReference", mock.Anything,
		mock.MatchedBy(func(tx *ValidationTransaction) bool { return tx == nil }),
		false, mock.Anything).
		Return(&ValidationResponse{HTTPStatus: 200, Payload: []byte(`{}`)}, nil)
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})

	_, err := svc.ValidateReference(context.Background(), "govpay", []byte(`{}`))
	require.NoError(t, err)
	gw.AssertExpectations(t)
}

func TestValidateReference_GatewayMismatch(t *testing.T) {
	repo := newMockRepo()
	repo.txs["TNSW1"] = &PaymentTransaction{
		ReferenceNumber: "TNSW1",
		GatewayID:       "other-gateway",
		Status:          PaymentStatusPending,
		ExpiryDate:      time.Now().Add(time.Hour),
	}
	gw := validateGateway("TNSW1")
	gw.On("HandleValidateReference", mock.Anything,
		mock.MatchedBy(func(tx *ValidationTransaction) bool { return tx == nil }),
		false, mock.Anything).
		Return(&ValidationResponse{HTTPStatus: 200, Payload: []byte(`{}`)}, nil)
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})

	_, err := svc.ValidateReference(context.Background(), "govpay", []byte(`{}`))
	require.NoError(t, err)
	gw.AssertExpectations(t)
}

func TestValidateReference_ExpiredNotPayable(t *testing.T) {
	repo := newMockRepo()
	repo.txs["TNSW1"] = &PaymentTransaction{
		ReferenceNumber: "TNSW1",
		GatewayID:       "govpay",
		Status:          PaymentStatusPending,
		ExpiryDate:      time.Now().Add(-time.Hour), // expired
	}
	gw := validateGateway("TNSW1")
	gw.On("HandleValidateReference", mock.Anything,
		mock.MatchedBy(func(tx *ValidationTransaction) bool { return tx != nil }),
		false, mock.Anything).
		Return(&ValidationResponse{HTTPStatus: 200, Payload: []byte(`{}`)}, nil)
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})

	_, err := svc.ValidateReference(context.Background(), "govpay", []byte(`{}`))
	require.NoError(t, err)
	gw.AssertExpectations(t)
}

// --- ProcessWebhook ---------------------------------------------------------

func pendingTx() *PaymentTransaction {
	return &PaymentTransaction{
		ReferenceNumber: "TNSW1",
		TaskID:          "task-9",
		GatewayID:       "govpay",
		Amount:          decimal.RequireFromString("1500.00"),
		Currency:        "LKR",
		Status:          PaymentStatusPending,
		ExpiryDate:      time.Now().Add(time.Hour),
	}
}

func webhookGateway(p *WebhookPayload) *MockGateway {
	gw := new(MockGateway)
	gw.On("ParseWebhook", mock.Anything, mock.Anything, mock.Anything).
		Return(p, &WebhookResponse{HTTPStatus: 200, Payload: []byte(`{"message":"Success"}`)}, nil)
	return gw
}

func TestProcessWebhook_SuccessAdvancesTask(t *testing.T) {
	repo := newMockRepo()
	repo.txs["TNSW1"] = pendingTx()
	gw := webhookGateway(&WebhookPayload{
		ReferenceNumber:      "TNSW1",
		Status:               WebhookStatusSuccess,
		Amount:               decimal.RequireFromString("1500.00"),
		Currency:             "LKR",
		GatewayTransactionID: "gw-tx-1",
		PaymentMethod:        "CC",
	})
	tc := &mockTaskCompleter{}
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})
	svc.SetTaskCompleter(tc)

	_, err := svc.ProcessWebhook(context.Background(), "govpay", []byte(`{}`), nil)
	require.NoError(t, err)
	assert.Equal(t, PaymentStatusSuccess, repo.txs["TNSW1"].Status)
	require.Len(t, tc.calls, 1)
	assert.Equal(t, "task-9", tc.calls[0].taskID)
	assert.Equal(t, "success", tc.calls[0].payload["payment_status"])
	// Completion carries the settled transaction's facts for completion-state UIs.
	assert.Equal(t, "TNSW1", tc.calls[0].payload["reference_number"])
	assert.Equal(t, "1500", tc.calls[0].payload["amount"])
	assert.Equal(t, "LKR", tc.calls[0].payload["currency"])
}

func TestProcessWebhook_AmountMismatch(t *testing.T) {
	repo := newMockRepo()
	repo.txs["TNSW1"] = pendingTx()
	gw := webhookGateway(&WebhookPayload{
		ReferenceNumber: "TNSW1",
		Status:          WebhookStatusSuccess,
		Amount:          decimal.RequireFromString("999.00"), // mismatch
		Currency:        "LKR",
	})
	tc := &mockTaskCompleter{}
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})
	svc.SetTaskCompleter(tc)

	_, err := svc.ProcessWebhook(context.Background(), "govpay", []byte(`{}`), nil)
	require.ErrorIs(t, err, ErrAmountMismatch)
	assert.Equal(t, PaymentStatusPending, repo.txs["TNSW1"].Status, "must not mark paid on mismatch")
	assert.Empty(t, tc.calls)
}

func TestProcessWebhook_CurrencyMismatch(t *testing.T) {
	repo := newMockRepo()
	repo.txs["TNSW1"] = pendingTx()
	gw := webhookGateway(&WebhookPayload{
		ReferenceNumber: "TNSW1",
		Status:          WebhookStatusSuccess,
		Amount:          decimal.RequireFromString("1500.00"),
		Currency:        "USD", // mismatch
	})
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})

	_, err := svc.ProcessWebhook(context.Background(), "govpay", []byte(`{}`), nil)
	require.ErrorIs(t, err, ErrAmountMismatch)
	assert.Equal(t, PaymentStatusPending, repo.txs["TNSW1"].Status)
}

func TestProcessWebhook_Failed(t *testing.T) {
	repo := newMockRepo()
	repo.txs["TNSW1"] = pendingTx()
	gw := webhookGateway(&WebhookPayload{
		ReferenceNumber: "TNSW1",
		Status:          WebhookStatusFailed,
	})
	tc := &mockTaskCompleter{}
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})
	svc.SetTaskCompleter(tc)

	_, err := svc.ProcessWebhook(context.Background(), "govpay", []byte(`{}`), nil)
	require.NoError(t, err)
	assert.Equal(t, PaymentStatusFailed, repo.txs["TNSW1"].Status)
	require.Len(t, tc.calls, 1)
	assert.Equal(t, "fail", tc.calls[0].payload["payment_status"])
}

func TestProcessWebhook_Idempotent(t *testing.T) {
	repo := newMockRepo()
	settled := pendingTx()
	settled.Status = PaymentStatusSuccess
	repo.txs["TNSW1"] = settled
	gw := webhookGateway(&WebhookPayload{
		ReferenceNumber: "TNSW1",
		Status:          WebhookStatusSuccess,
		Amount:          decimal.RequireFromString("1500.00"),
		Currency:        "LKR",
	})
	tc := &mockTaskCompleter{}
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})
	svc.SetTaskCompleter(tc)

	_, err := svc.ProcessWebhook(context.Background(), "govpay", []byte(`{}`), nil)
	require.NoError(t, err)
	assert.Empty(t, tc.calls, "already-terminal webhook must not advance the task again")
}

func TestProcessWebhook_NotFound(t *testing.T) {
	repo := newMockRepo()
	gw := webhookGateway(&WebhookPayload{ReferenceNumber: "NOPE", Status: WebhookStatusSuccess})
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})

	_, err := svc.ProcessWebhook(context.Background(), "govpay", []byte(`{}`), nil)
	require.ErrorIs(t, err, ErrTransactionNotFound)
}

func TestProcessWebhook_UnsupportedStatus(t *testing.T) {
	repo := newMockRepo()
	repo.txs["TNSW1"] = pendingTx()
	gw := webhookGateway(&WebhookPayload{
		ReferenceNumber: "TNSW1",
		Status:          WebhookStatus("WEIRD"),
	})
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})

	_, err := svc.ProcessWebhook(context.Background(), "govpay", []byte(`{}`), nil)
	require.ErrorIs(t, err, ErrUnsupportedWebhookStatus)
	assert.Equal(t, PaymentStatusPending, repo.txs["TNSW1"].Status)
}

func TestProcessWebhook_NonTerminalDoesNotAdvance(t *testing.T) {
	repo := newMockRepo()
	repo.txs["TNSW1"] = pendingTx()
	gw := webhookGateway(&WebhookPayload{
		ReferenceNumber: "TNSW1",
		Status:          WebhookStatusPending,
	})
	tc := &mockTaskCompleter{}
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})
	svc.SetTaskCompleter(tc)

	_, err := svc.ProcessWebhook(context.Background(), "govpay", []byte(`{}`), nil)
	require.NoError(t, err)
	assert.Empty(t, tc.calls, "a PENDING webhook must not advance the task")
}

func TestProcessWebhook_CompleterErrorPropagates(t *testing.T) {
	repo := newMockRepo()
	repo.txs["TNSW1"] = pendingTx()
	gw := webhookGateway(&WebhookPayload{
		ReferenceNumber: "TNSW1",
		Status:          WebhookStatusSuccess,
		Amount:          decimal.RequireFromString("1500.00"),
		Currency:        "LKR",
	})
	tc := &mockTaskCompleter{err: errors.New("task engine down")}
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})
	svc.SetTaskCompleter(tc)

	_, err := svc.ProcessWebhook(context.Background(), "govpay", []byte(`{}`), nil)
	require.Error(t, err)
	assert.Equal(t, PaymentStatusSuccess, repo.txs["TNSW1"].Status, "status is committed before the advance call")
}

func TestListAvailableMethods(t *testing.T) {
	infos := []GatewayInfo{{ID: "govpay", IsActive: true}}
	svc := NewPaymentService(newMockRepo(), &mockRegistry{infos: infos})

	got, err := svc.ListAvailableMethods(context.Background())
	require.NoError(t, err)
	assert.Equal(t, infos, got)
}

func TestCreateCheckoutSession_ReferenceLookupError(t *testing.T) {
	repo := newMockRepo()
	repo.getErr = errors.New("db down")
	gw := new(MockGateway) // CreateSession must not be reached
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})

	_, err := svc.CreateCheckoutSession(context.Background(), validCheckoutReq())
	require.Error(t, err)
	gw.AssertNotCalled(t, "CreateSession", mock.Anything, mock.Anything)
}

func TestCreateCheckoutSession_SessionIDPersistError(t *testing.T) {
	repo := newMockRepo()
	repo.updateErr = errors.New("db down") // the session-id Update fails
	gw := new(MockGateway)
	gw.On("CreateSession", mock.Anything, mock.Anything).
		Return(&SessionResponse{SessionID: "sess-1"}, nil)
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})

	_, err := svc.CreateCheckoutSession(context.Background(), validCheckoutReq())
	require.Error(t, err)
}

func TestCreateCheckoutSession_GatewayErrorAndMarkFailedAlsoErrors(t *testing.T) {
	repo := newMockRepo()
	repo.updateErr = errors.New("db down") // the mark-FAILED Update also fails
	gw := new(MockGateway)
	gw.On("CreateSession", mock.Anything, mock.Anything).Return(nil, errors.New("boom"))
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})

	_, err := svc.CreateCheckoutSession(context.Background(), validCheckoutReq())
	require.Error(t, err) // still surfaces the gateway error; mark-FAILED failure is only logged
}

func TestValidateReference_GatewayNotFound(t *testing.T) {
	svc := NewPaymentService(newMockRepo(), &mockRegistry{getErr: errors.New("nope")})
	_, err := svc.ValidateReference(context.Background(), "govpay", []byte(`{}`))
	require.Error(t, err)
}

func TestValidateReference_ExtractError(t *testing.T) {
	gw := new(MockGateway)
	gw.On("ExtractReferenceNumber", mock.Anything, mock.Anything).Return("", errors.New("bad body"))
	svc := NewPaymentService(newMockRepo(), &mockRegistry{gw: gw})

	_, err := svc.ValidateReference(context.Background(), "govpay", []byte(`{}`))
	require.Error(t, err)
}

func TestValidateReference_RepoError(t *testing.T) {
	repo := newMockRepo()
	repo.getErr = errors.New("db down")
	svc := NewPaymentService(repo, &mockRegistry{gw: validateGateway("TNSW1")})

	_, err := svc.ValidateReference(context.Background(), "govpay", []byte(`{}`))
	require.Error(t, err)
}

func TestProcessWebhook_GatewayNotFound(t *testing.T) {
	svc := NewPaymentService(newMockRepo(), &mockRegistry{getErr: errors.New("nope")})
	_, err := svc.ProcessWebhook(context.Background(), "govpay", []byte(`{}`), nil)
	require.Error(t, err)
}

func TestProcessWebhook_ParseError(t *testing.T) {
	gw := new(MockGateway)
	gw.On("ParseWebhook", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil, errors.New("bad payload"))
	svc := NewPaymentService(newMockRepo(), &mockRegistry{gw: gw})

	_, err := svc.ProcessWebhook(context.Background(), "govpay", []byte(`{}`), nil)
	require.Error(t, err)
}

func TestProcessWebhook_RepoGetError(t *testing.T) {
	repo := newMockRepo()
	repo.getErr = errors.New("db down")
	gw := webhookGateway(&WebhookPayload{ReferenceNumber: "TNSW1", Status: WebhookStatusSuccess})
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})

	_, err := svc.ProcessWebhook(context.Background(), "govpay", []byte(`{}`), nil)
	require.Error(t, err)
}

func TestProcessWebhook_UpdateError(t *testing.T) {
	repo := newMockRepo()
	repo.txs["TNSW1"] = pendingTx()
	repo.updateErr = errors.New("db down")
	gw := webhookGateway(&WebhookPayload{
		ReferenceNumber: "TNSW1",
		Status:          WebhookStatusSuccess,
		Amount:          decimal.RequireFromString("1500.00"),
		Currency:        "LKR",
	})
	svc := NewPaymentService(repo, &mockRegistry{gw: gw})

	_, err := svc.ProcessWebhook(context.Background(), "govpay", []byte(`{}`), nil)
	require.Error(t, err)
}
