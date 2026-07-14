package profile

import (
	"hash/fnv"
	"math"
	"math/bits"
)

// hash64 is a fixed-seed deterministic 64-bit hash: FNV-1a plus a murmur3
// fmix64 finalizer that fixes FNV's weak low-bit distribution. It uses no
// per-process seed (hash/maphash is deliberately avoided) so output is
// reproducible across runs and machines.
func hash64(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	x := h.Sum64()
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}

// hll is a HyperLogLog cardinality estimator with dense registers. It is only
// used for cardinalities above the accumulator's exactCap (>= 2.5*2^p), so no
// small-range/linear-counting correction is needed.
type hll struct {
	p    uint
	regs []uint8
}

func newHLL(p uint) *hll {
	return &hll{p: p, regs: make([]uint8, 1<<p)}
}

func (h *hll) add(key string) {
	x := hash64(key)
	idx := x >> (64 - h.p)             // top p bits -> register index
	w := (x << h.p) | (1 << (h.p - 1)) // remaining bits; guard bit bounds the rank
	rank := uint8(bits.LeadingZeros64(w)) + 1
	if rank > h.regs[idx] {
		h.regs[idx] = rank
	}
}

func hllAlpha(m float64) float64 {
	switch m {
	case 16:
		return 0.673
	case 32:
		return 0.697
	case 64:
		return 0.709
	default:
		return 0.7213 / (1 + 1.079/m)
	}
}

func (h *hll) estimate() int {
	m := float64(len(h.regs))
	sum := 0.0
	for _, r := range h.regs {
		sum += math.Ldexp(1, -int(r)) // 2^-r
	}
	return int(hllAlpha(m)*m*m/sum + 0.5)
}
