package idempotency

import (
	"bytes"
	"testing"
)

func TestRequestHashIsCanonical(t *testing.T) {
	a, err := RequestHash("post", "/api/v1/receipts/1:post", []byte(`{"b": 1.50, "a": [1, {"y":2,"x":1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := RequestHash("POST", "/api/v1/receipts/1:post", []byte("{\"a\":[1,{\"x\":1,\"y\":2}],\n \"b\":1.50}"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("same request with different key order and whitespace hashes differently")
	}
	if len(a) != 32 {
		t.Fatalf("hash length %d", len(a))
	}
	c, _ := RequestHash("POST", "/api/v1/receipts/1:post", []byte(`{"a":[1,{"x":1,"y":2}],"b":1.5}`))
	if bytes.Equal(a, c) {
		t.Fatal("1.50 and 1.5 must hash differently (numbers are kept verbatim)")
	}
	d, _ := RequestHash("POST", "/api/v1/receipts/2:post", []byte(`{"a":[1,{"x":1,"y":2}],"b":1.50}`))
	if bytes.Equal(a, d) {
		t.Fatal("different path, same hash")
	}
	empty1, _ := RequestHash("POST", "/x", nil)
	empty2, _ := RequestHash("POST", "/x", []byte("  \n"))
	if !bytes.Equal(empty1, empty2) {
		t.Fatal("empty bodies hash differently")
	}
}

func TestRequestHashRejectsInvalidJSON(t *testing.T) {
	for _, body := range []string{`{"a":`, `{"a":1} {"b":2}`} {
		if _, err := RequestHash("POST", "/x", []byte(body)); err == nil {
			t.Errorf("body %q accepted", body)
		}
	}
}

func TestKeyValid(t *testing.T) {
	h, _ := RequestHash("POST", "/x", nil)
	cases := []struct {
		key  Key
		want bool
	}{
		{Key{Value: "k1", RequestHash: h}, true},
		{Key{Value: "", RequestHash: h}, false},
		{Key{Value: "k1", RequestHash: h[:10]}, false},
	}
	for _, tc := range cases {
		if got := tc.key.Valid(); got != tc.want {
			t.Errorf("Valid(%+v) = %v", tc.key, got)
		}
	}
}
