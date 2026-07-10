package edm

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// checkUser queries GİB whether the VKN is registered for e-invoice.
// It handles session expiry transparently with a single retry.
func (a *Adapter) checkUser(ctx context.Context, tenantID uuid.UUID, vkn string) (*userResult, error) {
	sessionID, err := a.sessions.getOrCreate(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return a.checkUserWithSession(ctx, tenantID, sessionID, vkn, true)
}

func (a *Adapter) checkUserWithSession(ctx context.Context, tenantID uuid.UUID, sessionID, vkn string, retry bool) (*userResult, error) {
	_ = ctx // context passed through HTTP client implicitly via timeout
	bodyXML := fmt.Sprintf(`<CheckUserRequest xmlns="http://tempuri.org/">%s`+
		`<USER xmlns=""><IDENTIFIER>%s</IDENTIFIER></USER>`+
		`</CheckUserRequest>`,
		requestHeader(sessionID), escapeXML(vkn))

	respBody, err := a.c.call(actionCheckUser, bodyXML)
	if err != nil {
		if errors.Is(err, ErrSessionExpired) && retry {
			a.sessions.invalidate(ctx, tenantID)
			newSession, loginErr := a.sessions.getOrCreate(ctx, tenantID)
			if loginErr != nil {
				return nil, loginErr
			}
			return a.checkUserWithSession(ctx, tenantID, newSession, vkn, false)
		}
		return nil, err
	}

	respStr := string(respBody)
	alias := extractXMLValue(respStr, "ALIAS")
	identifier := extractXMLValue(respStr, "IDENTIFIER")

	if alias == "" && identifier == "" {
		return nil, ErrNotRegistered
	}

	// Prefer PK (posta kutusu) over GB (genel bildirim) aliases.
	if extractXMLValue(respStr, "UNIT") == "GB" {
		if idx := strings.Index(respStr, "<UNIT>PK</UNIT>"); idx > 0 {
			before := respStr[:idx]
			if last := strings.LastIndex(before, "<ALIAS>"); last > 0 {
				if end := strings.Index(before[last:], "</ALIAS>"); end > 0 {
					alias = before[last+7 : last+end]
				}
			}
		}
	}

	return &userResult{
		Identifier: identifier,
		Alias:      alias,
		Title:      extractXMLValue(respStr, "TITLE"),
		Unit:       extractXMLValue(respStr, "UNIT"),
	}, nil
}

// sendInvoice submits a base64-encoded UBL XML invoice to EDM.
func (a *Adapter) sendInvoice(
	ctx context.Context,
	tenantID uuid.UUID,
	senderAlias, receiverAlias string,
	supplierVKN, customerVKN string,
	invoiceUUID string,
	xmlContent []byte,
	isEArchive bool,
) (*requestReturn, error) {
	sessionID, err := a.sessions.getOrCreate(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return a.sendInvoiceWithSession(ctx, tenantID, sessionID, senderAlias, receiverAlias, supplierVKN, customerVKN, invoiceUUID, xmlContent, isEArchive, true)
}

func (a *Adapter) sendInvoiceWithSession(
	ctx context.Context,
	tenantID uuid.UUID,
	sessionID, senderAlias, receiverAlias, supplierVKN, customerVKN, invoiceUUID string,
	xmlContent []byte,
	isEArchive, retry bool,
) (*requestReturn, error) {
	_ = ctx
	encoded := base64.StdEncoding.EncodeToString(xmlContent)
	eArchiveFlag := "false"
	if isEArchive {
		eArchiveFlag = "true"
	}

	bodyXML := fmt.Sprintf(`<SendInvoiceRequest xmlns="http://tempuri.org/">%s`+
		`<RECEIVER vkn="%s" alias="%s" xmlns=""/>`+
		`<INVOICE TRXID="0" xmlns="">`+
		`<HEADER><SENDER>%s</SENDER><RECEIVER>%s</RECEIVER><FROM>%s</FROM><TO>%s</TO>`+
		`<INTERNETSALES>false</INTERNETSALES><EARCHIVE>%s</EARCHIVE></HEADER>`+
		`<CONTENT>%s</CONTENT></INVOICE>`+
		`</SendInvoiceRequest>`,
		requestHeader(sessionID),
		escapeXML(customerVKN), escapeXML(receiverAlias),
		escapeXML(supplierVKN), escapeXML(customerVKN),
		escapeXML(senderAlias), escapeXML(receiverAlias),
		eArchiveFlag, encoded,
	)

	respBody, err := a.c.call(actionSendInvoice, bodyXML)
	if err != nil {
		if errors.Is(err, ErrSessionExpired) && retry {
			a.sessions.invalidate(ctx, tenantID)
			newSession, loginErr := a.sessions.getOrCreate(ctx, tenantID)
			if loginErr != nil {
				return nil, loginErr
			}
			return a.sendInvoiceWithSession(ctx, tenantID, newSession, senderAlias, receiverAlias, supplierVKN, customerVKN, invoiceUUID, xmlContent, isEArchive, false)
		}
		return nil, err
	}

	respStr := string(respBody)
	if errCode := extractXMLValue(respStr, "ERROR_CODE"); errCode != "" {
		return nil, fmt.Errorf("%w: [%s] %s", ErrSOAPFault, errCode, extractXMLValue(respStr, "ERROR_LONG_DES"))
	}

	return &requestReturn{
		IntlTxnID:  extractXMLValue(respStr, "INTL_TXN_ID"),
		ReturnCode: extractXMLValue(respStr, "RETURN_CODE"),
	}, nil
}

// getInvoiceStatus retrieves the current GİB status of a submitted invoice.
func (a *Adapter) getInvoiceStatus(ctx context.Context, tenantID uuid.UUID, invoiceUUID string) (*invoiceStatusResult, error) {
	sessionID, err := a.sessions.getOrCreate(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return a.getInvoiceStatusWithSession(ctx, tenantID, sessionID, invoiceUUID, true)
}

func (a *Adapter) getInvoiceStatusWithSession(ctx context.Context, tenantID uuid.UUID, sessionID, invoiceUUID string, retry bool) (*invoiceStatusResult, error) {
	_ = ctx
	bodyXML := fmt.Sprintf(`<GetInvoiceStatusRequest xmlns="http://tempuri.org/">%s`+
		`<INVOICE TRXID="0" UUID="%s" xmlns=""/>`+
		`</GetInvoiceStatusRequest>`,
		requestHeader(sessionID), escapeXML(invoiceUUID))

	respBody, err := a.c.call(actionGetInvoiceStatus, bodyXML)
	if err != nil {
		if errors.Is(err, ErrSessionExpired) && retry {
			a.sessions.invalidate(ctx, tenantID)
			newSession, loginErr := a.sessions.getOrCreate(ctx, tenantID)
			if loginErr != nil {
				return nil, loginErr
			}
			return a.getInvoiceStatusWithSession(ctx, tenantID, newSession, invoiceUUID, false)
		}
		return nil, err
	}

	respStr := string(respBody)
	return &invoiceStatusResult{
		Status:     extractXMLValue(respStr, "STATUS"),
		StatusDesc: extractXMLValue(respStr, "STATUS_DESCRIPTION"),
	}, nil
}
