package ir

import (
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
)

type Number interface {
	TypeIsNumber()
}

type Int struct {
	*big.Int
}

func NewInt64(v int64) Int {
	return Int{big.NewInt(v)}
}

func (n Int) Disassemble() Node {
	return n
}

func (n Int) TypeIsNumber() {}

func (n Int) WritePretty(w io.Writer) (err error) {
	_, err = w.Write([]byte(n.Int.String()))
	return err
}

func (n Int) UpdateWith(ctx UpdateContext, with Node) (Node, error) {
	wn, ok := with.(Int)
	if !ok {
		return nil, fmt.Errorf("cannot update with different primitive type")
	}
	return wn, nil
}

type Float struct {
	*big.Float
}

func (n Float) Disassemble() Node {
	return n
}

func (n Float) TypeIsNumber() {}

func (n Float) WritePretty(w io.Writer) (err error) {
	_, err = w.Write([]byte(n.Float.String()))
	return err
}

func (n Float) UpdateWith(ctx UpdateContext, with Node) (Node, error) {
	wn, ok := with.(Float)
	if !ok {
		return nil, fmt.Errorf("cannot update with different primitive type")
	}
	return wn, nil
}

func IsEqualNumber(x, y Number) bool {
	switch x1 := x.(type) {
	case Int:
		switch y1 := y.(type) {
		case Int:
			return x1.Int.Cmp(y1.Int) == 0
		case Float:
			return false
		}
	case Float:
		switch y1 := y.(type) {
		case Int:
			return false
		case Float:
			return x1.Float.Cmp(y1.Float) == 0
		}
	}
	panic("bug: unknown number type")
}

func (n Int) EncodeJSON() (interface{}, error) {
	bn, err := n.MarshalText()
	if err != nil {
		return nil, err
	}
	return struct {
		Type  marshalType `json:"type"`
		Value []byte      `json:"value"`
	}{Type: IntType, Value: bn}, nil
}

func (n Float) EncodeJSON() (interface{}, error) {
	bn, err := n.MarshalText()
	if err != nil {
		return nil, err
	}
	return struct {
		Type  marshalType `json:"type"`
		Value []byte      `json:"value"`
	}{Type: FloatType, Value: bn}, nil
}

func decodeInt(s map[string]interface{}) (Node, error) {
	z := new(big.Int)
	r, ok := s["value"].(string)
	if !ok {
		return nil, fmt.Errorf("wrong int decoding type")
	}
	// Unmarshaller inteprets []byte as string, we need to decode base64
	sDec, err := base64.StdEncoding.DecodeString(r)
	if err != nil {
		return nil, err
	}
	err = z.UnmarshalText(sDec)
	if err != nil {
		return nil, err
	}
	return Int{z}, nil
}

func decodeFloat(s map[string]interface{}) (Node, error) {
	z := new(big.Float)
	r, ok := s["value"].(string)
	if !ok {
		return nil, fmt.Errorf("wrong float decoding type")
	}
	// Unmarshaller inteprets []byte as string, we need to decode base64
	sDec, err := base64.StdEncoding.DecodeString(r)
	if err != nil {
		return nil, err
	}
	err = z.UnmarshalText(sDec)
	if err != nil {
		return nil, err
	}
	return Float{z}, nil
}
