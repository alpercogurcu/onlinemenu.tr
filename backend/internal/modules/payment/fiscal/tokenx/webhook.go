package tokenx

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/payment/domain"
)

// Webhook operations delivered by Token X Connect Cloud.
const (
	OperationBasketCompleted = "BASKET_COMPLETED"
	OperationBasketLocked    = "BASKET_LOCKED"
	OperationBasketUnlocked  = "BASKET_UNLOCKED"
)

// BASKET_COMPLETED status codes.
const (
	statusCompleted = 0
	statusFailed    = -1
	statusVoided    = 99
)

// operationDateLayouts are tried in order; Token's documentation does not pin
// the format down.
var operationDateLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
}

type webhookEnvelope struct {
	TerminalID    string          `json:"terminalId"`
	ClientID      string          `json:"clientId"`
	Operation     string          `json:"operation"`
	OperationDate string          `json:"operationDate"`
	Data          json.RawMessage `json:"data"`
}

type completedData struct {
	BasketID     string               `json:"basketID"`
	DocumentType int                  `json:"documentType"`
	InvoiceID    string               `json:"invoiceID"`
	Message      string               `json:"message"`
	PaymentCount int                  `json:"paymentCount"`
	PaymentItems []webhookPaymentItem `json:"paymentItems"`
	ReceiptNo    int                  `json:"receiptNo"`
	Status       int                  `json:"status"`
	UUID         string               `json:"UUID"`
	ZNo          int                  `json:"zNo"`
}

// webhookPaymentItem mirrors Token's mixed-case field names verbatim.
type webhookPaymentItem struct {
	Amount      int64  `json:"amount"`
	BatchNo     string `json:"BatchNo"`
	CurrencyID  int    `json:"currencyId"`
	Description string `json:"description"`
	OperatorID  int    `json:"operatorId"`
	Status      int    `json:"status"`
	TxnNo       string `json:"TxnNo"`
	Type        int    `json:"type"`
}

type lockData struct {
	BasketID string `json:"basketID"`
}

// LockEvent reports that a terminal opened (locked) or released (unlocked) a
// basket. It never reaches FiscalResultSink — the payment is untouched — but
// the POS can surface "cashier is collecting payment" from it later.
type LockEvent struct {
	Operation    string
	Locked       bool
	TerminalID   string
	ClientID     string
	SubmissionID uuid.UUID
	OccurredAt   time.Time
	Raw          json.RawMessage
}

// WebhookOperation peeks at the envelope so the transport can route a payload
// to ParseWebhook or ParseLockEvent without decoding it twice.
func WebhookOperation(payload []byte) (string, error) {
	var env webhookEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return "", fmt.Errorf("tokenx: decode webhook envelope: %w", err)
	}
	if env.Operation == "" {
		return "", fmt.Errorf("tokenx: webhook carries no operation")
	}
	return env.Operation, nil
}

// ParseWebhook normalizes a BASKET_COMPLETED payload into a vendor-neutral
// FiscalResult. TenantID, BranchID and PaymentID stay zero: the webhook is
// tenant-agnostic and the worker fills them from fiscal_submissions keyed by
// SubmissionID.
func ParseWebhook(payload []byte) (domain.FiscalResult, error) {
	var env webhookEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return domain.FiscalResult{}, fmt.Errorf("tokenx: decode webhook envelope: %w", err)
	}
	if env.Operation != OperationBasketCompleted {
		return domain.FiscalResult{}, fmt.Errorf("%w: %q (want %s)", ErrUnexpectedOperation, env.Operation, OperationBasketCompleted)
	}

	var data completedData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return domain.FiscalResult{}, fmt.Errorf("tokenx: decode webhook data: %w", err)
	}

	submissionID, err := uuid.Parse(data.BasketID)
	if err != nil {
		return domain.FiscalResult{}, fmt.Errorf("tokenx: basketID %q is not a uuid: %w", data.BasketID, err)
	}

	status, err := submissionStatusOf(data.Status)
	if err != nil {
		return domain.FiscalResult{}, err
	}

	res := domain.FiscalResult{
		SubmissionID: submissionID,
		Status:       status,
		DeviceType:   DeviceType,
		VendorRef:    data.UUID,
		Payments:     confirmedPayments(data.PaymentItems),
		Raw:          json.RawMessage(payload),
		CompletedAt:  parseOperationDate(env.OperationDate),
	}
	// receiptNo/zNo are zero on a failed registration; emitting "0" would look
	// like a real receipt number, so they stay empty unless the sale completed.
	if data.ReceiptNo != 0 {
		res.ReceiptNo = strconv.Itoa(data.ReceiptNo)
	}
	if data.ZNo != 0 {
		res.ZNo = strconv.Itoa(data.ZNo)
	}
	if status == domain.FiscalSubmissionFailed {
		res.FailureReason = data.Message
	}
	return res, nil
}

// ParseLockEvent normalizes a BASKET_LOCKED / BASKET_UNLOCKED payload.
func ParseLockEvent(payload []byte) (LockEvent, error) {
	var env webhookEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return LockEvent{}, fmt.Errorf("tokenx: decode webhook envelope: %w", err)
	}
	if env.Operation != OperationBasketLocked && env.Operation != OperationBasketUnlocked {
		return LockEvent{}, fmt.Errorf("%w: %q (want %s or %s)",
			ErrUnexpectedOperation, env.Operation, OperationBasketLocked, OperationBasketUnlocked)
	}

	var data lockData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return LockEvent{}, fmt.Errorf("tokenx: decode webhook data: %w", err)
	}
	submissionID, err := uuid.Parse(data.BasketID)
	if err != nil {
		return LockEvent{}, fmt.Errorf("tokenx: basketID %q is not a uuid: %w", data.BasketID, err)
	}

	return LockEvent{
		Operation:    env.Operation,
		Locked:       env.Operation == OperationBasketLocked,
		TerminalID:   env.TerminalID,
		ClientID:     env.ClientID,
		SubmissionID: submissionID,
		OccurredAt:   parseOperationDate(env.OperationDate),
		Raw:          json.RawMessage(payload),
	}, nil
}

func submissionStatusOf(code int) (domain.FiscalSubmissionStatus, error) {
	switch code {
	case statusCompleted:
		return domain.FiscalSubmissionCompleted, nil
	case statusFailed:
		return domain.FiscalSubmissionFailed, nil
	case statusVoided:
		return domain.FiscalSubmissionVoided, nil
	default:
		return "", fmt.Errorf("%w: %d", ErrUnknownStatus, code)
	}
}

func confirmedPayments(items []webhookPaymentItem) []domain.FiscalConfirmedPayment {
	if len(items) == 0 {
		return nil
	}
	out := make([]domain.FiscalConfirmedPayment, 0, len(items))
	for _, it := range items {
		out = append(out, domain.FiscalConfirmedPayment{
			Method:      methodOf(it.Type),
			VendorType:  it.Type,
			AmountMinor: it.Amount,
			Description: it.Description,
		})
	}
	return out
}

// parseOperationDate falls back to "now" for an absent or unparseable date.
// The legal fields (receiptNo, zNo, UUID) and the raw payload are preserved
// either way, so rejecting the whole registration over a timestamp format
// would lose far more than it protects.
func parseOperationDate(s string) time.Time {
	for _, layout := range operationDateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}
