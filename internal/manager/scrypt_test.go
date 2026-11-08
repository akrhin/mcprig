package manager

import (
	"encoding/hex"
	"testing"
)

// Векторы RFC 7914, раздел 12: ими проверяется и сам scrypt, и формат пароля.
func TestScryptVectors(t *testing.T) {
	cases := []struct {
		password, salt string
		n, r, p        int
		want           string
	}{
		{"", "", 16, 1, 1,
			"77d6576238657b203b19ca42c18a0497f16b4844e3074ae8dfdffa3fede21442" +
				"fcd0069ded0948f8326a753a0fc81f17e8d3e0fb2e0d3628cf35e20c38d18906"},
		{"password", "NaCl", 1024, 8, 16,
			"fdbabe1c9d3472007856e7190d01e9fe7c6ad7cbc8237830e77376634b373162" +
				"2eaf30d92e22a3886ff109279d9830dac727afb94a83ee6d8360cbdfa2cc0640"},
		{"pleaseletmein", "SodiumChloride", 16384, 8, 1,
			"7023bdcb3afd7348461c06cd81fd38ebfda8fbba904f8e3ea9b543f6545da1f2" +
				"d5432955613f0fcf62d49705242a9af9e61e85dc0d651e40dfcf017b45575887"},
	}
	for _, c := range cases {
		got, err := scryptKey([]byte(c.password), []byte(c.salt), c.n, c.r, c.p, 64)
		if err != nil {
			t.Fatalf("scrypt(N=%d): %v", c.n, err)
		}
		if hex.EncodeToString(got) != c.want {
			t.Errorf("scrypt(%q, N=%d) = %s, ожидалось %s", c.password, c.n, hex.EncodeToString(got), c.want)
		}
	}
}

func TestPasswordHash(t *testing.T) {
	encoded, err := hashPassword("pw")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	h, err := parseScryptHash(encoded)
	if err != nil {
		t.Fatalf("parseScryptHash(%q): %v", encoded, err)
	}
	if !h.verify("pw") {
		t.Error("верный пароль не прошёл проверку")
	}
	if h.verify("PW") {
		t.Error("неверный пароль прошёл проверку")
	}
	if _, err := parseScryptHash("plain"); err == nil {
		t.Error("формат без префикса scrypt принят")
	}
}
