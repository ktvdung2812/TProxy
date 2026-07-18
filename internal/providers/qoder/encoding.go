package qoder

import (
	"encoding/base64"
)

const (
	stdAlphabet    = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	customAlphabet = "_doRTgHZBKcGVjlvpC,@aFSx#DPuNJme&i*MzLOEn)sUrthbf%Y^w.(kIQyXqWA!"
)

var s2c [128]int16

func init() {
	for i := range s2c {
		s2c[i] = -1
	}
	for i := 0; i < 64; i++ {
		s2c[stdAlphabet[i]] = int16(customAlphabet[i])
	}
	s2c['='] = int16('$')
}

// EncodeBody applies Qoder's WAF-bypass body encoding.
func EncodeBody(plaintext []byte) string {
	std := base64.StdEncoding.EncodeToString(plaintext)
	n := len(std)
	a := n / 3
	rearranged := std[n-a:] + std[a:n-a] + std[:a]
	out := make([]byte, len(rearranged))
	for i := 0; i < len(rearranged); i++ {
		c := rearranged[i]
		if c < 128 && s2c[c] >= 0 {
			out[i] = byte(s2c[c])
		} else {
			out[i] = c
		}
	}
	return string(out)
}
