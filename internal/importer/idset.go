package importer

// Each word of the bitset holds 64 ids. The shift selects the word and the
// mask selects the bit inside it.
const (
	wordShift = 6
	bitMask   = 63
)

// idSet is a growable bitset keyed by fdc_id. The ids are dense integers
// below three million, so this is about 350 KB where a map would be tens of
// megabytes.
type idSet struct {
	bits  []uint64
	count int
}

func (set *idSet) Add(fdcID int64) {
	if fdcID < 0 {
		return
	}

	word := int(fdcID >> wordShift)
	if word >= len(set.bits) {
		grown := make([]uint64, word+1+word/2)
		copy(grown, set.bits)
		set.bits = grown
	}

	mask := uint64(1) << (fdcID & bitMask)
	if set.bits[word]&mask == 0 {
		set.bits[word] |= mask
		set.count++
	}
}

func (set *idSet) Has(fdcID int64) bool {
	if fdcID < 0 {
		return false
	}

	word := int(fdcID >> wordShift)
	if word >= len(set.bits) {
		return false
	}

	return set.bits[word]&(uint64(1)<<(fdcID&bitMask)) != 0
}

func (set *idSet) Len() int {
	return set.count
}
