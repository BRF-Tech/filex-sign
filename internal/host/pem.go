package host

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
)

func parsePEM(text string) (*x509.Certificate, error) {
	blk, _ := pem.Decode([]byte(text))
	if blk == nil {
		return nil, errors.New("certificate unreadable")
	}
	return x509.ParseCertificate(blk.Bytes)
}

func parseChain(text string) []*x509.Certificate {
	var out []*x509.Certificate
	rest := []byte(text)
	for {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			return out
		}
		if blk.Type != "CERTIFICATE" {
			continue
		}
		if c, err := x509.ParseCertificate(blk.Bytes); err == nil {
			out = append(out, c)
		}
	}
}
