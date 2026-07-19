package edm

// SOAP action constants matching EDM Bilisim WSDL soapAction values.
const (
	actionLogin            = "LoginRequest"
	actionLogout           = "LogoutRequest"
	actionCheckUser        = "CheckUserRequest"
	actionSendInvoice      = "SendInvoiceRequest"
	actionGetInvoiceStatus = "GetInvoiceStatusRequest"
)

// userResult holds a GİB mailbox alias lookup result.
type userResult struct {
	Identifier string
	Alias      string
	Title      string
	Unit       string
}

// requestReturn is the common RETURN block from EDM send operations.
type requestReturn struct {
	IntlTxnID  string
	ReturnCode string
}

// invoiceStatusResult holds the GET_INVOICE_STATUS response fields.
type invoiceStatusResult struct {
	Status     string
	StatusDesc string
}
