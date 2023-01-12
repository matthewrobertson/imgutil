package layout

import (
	"crypto/sha256"
	"fmt"

	v1 "github.com/google/go-containerregistry/pkg/v1"
)

type DigestIdentifier struct {
	Digest string
}

func newDigestIdentifier(configFile *v1.ConfigFile) (DigestIdentifier, error) {
	return DigestIdentifier{
		Digest: "sha256:" + asSha256(configFile),
	}, nil
}

func (i DigestIdentifier) String() string {
	return i.Digest
}

func asSha256(o interface{}) string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%v", o)))
	return fmt.Sprintf("%x", h.Sum(nil))
}
