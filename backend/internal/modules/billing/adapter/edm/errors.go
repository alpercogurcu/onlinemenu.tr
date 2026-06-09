package edm

import "errors"

var (
	ErrSessionExpired = errors.New("edm: session expired or not found")
	ErrSOAPFault      = errors.New("edm: SOAP fault")
	ErrEDMConnection  = errors.New("edm: connection error")
	ErrNotRegistered  = errors.New("edm: VKN not registered for e-invoice")
)
