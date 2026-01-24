// Package ulid provides ULID (Universally Unique Lexicographically Sortable Identifier) generation.
// ULIDs are used for event_id, gate_id, and other identifiers that benefit from sortability.
package ulid

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

// Generator generates ULIDs.
// It is safe for concurrent use.
type Generator struct {
	mu      sync.Mutex
	lastMS  int64
	lastSeq uint16
}

// DefaultGenerator is the default ULID generator.
var DefaultGenerator = &Generator{}

// New generates a new ULID string.
// ULIDs are 26 characters, lexicographically sortable, and use Crockford's Base32.
func New() string {
	return DefaultGenerator.New()
}

// New generates a new ULID string.
func (g *Generator) New() string {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now().UnixMilli()

	// Handle same-millisecond generation
	if now == g.lastMS {
		g.lastSeq++
		if g.lastSeq == 0 {
			// Overflow, wait for next millisecond
			for now <= g.lastMS {
				time.Sleep(time.Millisecond)
				now = time.Now().UnixMilli()
			}
			g.lastSeq = 0
		}
	} else {
		g.lastMS = now
		g.lastSeq = 0
	}

	return encode(uint64(now), g.lastSeq)
}

// encode generates a ULID from timestamp and sequence.
// ULID format: 10 chars timestamp + 16 chars randomness = 26 chars total.
func encode(timestamp uint64, seq uint16) string {
	// ULID uses Crockford's Base32 encoding
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

	var ulid [26]byte

	// Encode timestamp (48 bits = 10 characters)
	ulid[0] = alphabet[(timestamp>>45)&0x1F]
	ulid[1] = alphabet[(timestamp>>40)&0x1F]
	ulid[2] = alphabet[(timestamp>>35)&0x1F]
	ulid[3] = alphabet[(timestamp>>30)&0x1F]
	ulid[4] = alphabet[(timestamp>>25)&0x1F]
	ulid[5] = alphabet[(timestamp>>20)&0x1F]
	ulid[6] = alphabet[(timestamp>>15)&0x1F]
	ulid[7] = alphabet[(timestamp>>10)&0x1F]
	ulid[8] = alphabet[(timestamp>>5)&0x1F]
	ulid[9] = alphabet[timestamp&0x1F]

	// Generate random bytes for the remaining 80 bits (16 characters)
	var randomBytes [10]byte
	rand.Read(randomBytes[:])

	// Encode the sequence into the first 2 bytes for monotonicity within same ms
	binary.BigEndian.PutUint16(randomBytes[:2], seq)

	// Encode randomness (80 bits = 16 characters)
	ulid[10] = alphabet[(randomBytes[0]>>3)&0x1F]
	ulid[11] = alphabet[((randomBytes[0]<<2)|(randomBytes[1]>>6))&0x1F]
	ulid[12] = alphabet[(randomBytes[1]>>1)&0x1F]
	ulid[13] = alphabet[((randomBytes[1]<<4)|(randomBytes[2]>>4))&0x1F]
	ulid[14] = alphabet[((randomBytes[2]<<1)|(randomBytes[3]>>7))&0x1F]
	ulid[15] = alphabet[(randomBytes[3]>>2)&0x1F]
	ulid[16] = alphabet[((randomBytes[3]<<3)|(randomBytes[4]>>5))&0x1F]
	ulid[17] = alphabet[randomBytes[4]&0x1F]
	ulid[18] = alphabet[(randomBytes[5]>>3)&0x1F]
	ulid[19] = alphabet[((randomBytes[5]<<2)|(randomBytes[6]>>6))&0x1F]
	ulid[20] = alphabet[(randomBytes[6]>>1)&0x1F]
	ulid[21] = alphabet[((randomBytes[6]<<4)|(randomBytes[7]>>4))&0x1F]
	ulid[22] = alphabet[((randomBytes[7]<<1)|(randomBytes[8]>>7))&0x1F]
	ulid[23] = alphabet[(randomBytes[8]>>2)&0x1F]
	ulid[24] = alphabet[((randomBytes[8]<<3)|(randomBytes[9]>>5))&0x1F]
	ulid[25] = alphabet[randomBytes[9]&0x1F]

	return string(ulid[:])
}

// MustParse validates a ULID string format.
// Returns true if the string is a valid 26-character ULID.
func IsValid(s string) bool {
	if len(s) != 26 {
		return false
	}
	for _, c := range s {
		if !isValidChar(byte(c)) {
			return false
		}
	}
	return true
}

func isValidChar(c byte) bool {
	switch {
	case c >= '0' && c <= '9':
		return true
	case c >= 'A' && c <= 'H':
		return true
	case c >= 'J' && c <= 'N':
		return true
	case c >= 'P' && c <= 'T':
		return true
	case c >= 'V' && c <= 'Z':
		return true
	default:
		return false
	}
}
