package exception

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestPayloadHashIsCanonical(t *testing.T) {
	a, err := PayloadHash([]byte(`{"b": "1", "a": {"y": 2, "x": [1, "<&>"]}, "exception_confirmation_id": "x"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := PayloadHash([]byte("{\"a\":{\"x\":[1,\"<&>\"],\"y\":2},\"b\":\"1\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("key order, whitespace and the confirmation field must not change the hash")
	}
	c, err := PayloadHash([]byte(`{"a":{"x":[1,"<&>"],"y":2},"b":"2"}`))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, c) {
		t.Fatal("a different value must change the hash")
	}
	// Numbers are kept as sent: 1.0 and 1 are different payloads.
	d1, _ := PayloadHash([]byte(`{"n":1.0}`))
	d2, _ := PayloadHash([]byte(`{"n":1}`))
	if bytes.Equal(d1, d2) {
		t.Fatal("number text must be preserved")
	}
}

// The web client hashes canonicalJson(JSON.parse(body)) (shared/lib/canonical-hash.ts); these
// digests were computed with that function on Node. Each case covers a place where Go's
// encoding/json output differs from JSON.stringify: U+2028/U+2029 and HTML characters stay
// literal, DEL stays literal, control characters use short escapes, and keys sort by UTF-16 code
// units (an emoji key sorts before U+FF61).
func TestPayloadHashMatchesTheWebClient(t *testing.T) {
	cases := []struct{ body, want string }{
		{`{"note":"line\u2028sep\u2029end","b":"<&>","a":1}`, "d9c54546080cbb6b9d8d729d01ee5686d54c8a0f1ff35e6e98f2d465cbf5e474"},
		{`{"\ud83d\ude00":1,"\uff61":2,"z":"\u0000\u001f\u007f\b\f\n\r\t\"\\/"}`, "25d42a4a9d3523ce69f7f5fdcc188d3b5c75d23b3791312ab47e2c725ea22604"},
		{`{"items":[{"qty":"1.50","sku":"Áo len đỏ"},null,true,false,-0.5,1e+21],"exception_confirmation_id":"x"}`, "008a777fe7dbf7259c14df12babf568324cf034559404cd348cda5694cfa69f2"},
	}
	for _, c := range cases {
		got, err := PayloadHash([]byte(c.body))
		if err != nil {
			t.Fatalf("%s: %v", c.body, err)
		}
		if hex.EncodeToString(got) != c.want {
			var buf bytes.Buffer
			var v any
			dec := json.NewDecoder(strings.NewReader(c.body))
			dec.UseNumber()
			_ = dec.Decode(&v)
			_ = writeCanonical(&buf, v)
			t.Errorf("%s: hash %x, want %s (canonical %s)", c.body, got, c.want, buf.String())
		}
	}
}

func TestPayloadHashRejectsInvalidJSON(t *testing.T) {
	for _, in := range []string{``, `{`, `{"a":1} {"b":2}`, `nope`} {
		if _, err := PayloadHash([]byte(in)); err == nil {
			t.Errorf("PayloadHash(%q) succeeded", in)
		}
	}
}

func TestKinds(t *testing.T) {
	if !PeriodReopen.Valid() || Kind("other").Valid() || len(Kinds()) != 8 {
		t.Fatal("kind list")
	}
}
