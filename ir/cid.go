package ir

import (
	"io"
)

// Cid is a smart node, representing a valid CID.
type Cid struct {
	Cid string

	// User holds user fields.
	User Dict
}

func (c Cid) Dict() Dict {
	return c.User.CopySetTag("cid", String{c.Cid}, String{c.Cid})
}

func (c Cid) WritePretty(w io.Writer) error {
	return c.Dict().WritePretty(w)
}

func (c Cid) MergeWith(ctx MergeContext, x Node) Node {
	panic("XXX")
}
