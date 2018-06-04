package parquet

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Implementation of RLE/Bit-Packing Hybrid encoding

// encoded-data := <run>*
// run := <bit-packed-run> | <rle-run>
// bit-packed-run := <bit-packed-header> <bit-packed-values>
// bit-packed-header := varint-encode(<bit-pack-count> << 1 | 1)
//  (we always bit-pack a multiple of 8 values at a time, so we only store the number of values / 8)
// bit-pack-count := (number of values in this run) / 8
// bit-packed-values := bit packed values
// rle-run := <rle-header> <repeated-value>
// rle-header := varint-encode( (number of times repeated) << 1)
// repeated-value := value that is repeated, using a fixed-width of round-up-to-next-byte(bit-width)

type rle32Decoder struct {
	bitWidth   int
	byteWidth  int
	bpUnpacker unpack8int32Func

	data  []byte
	count int
	i     int
	pos   int

	// rle
	rleCount uint32
	rleValue int32

	// bit-packed
	bpCount  uint32
	bpRunPos uint8
	bpRun    [8]int32
}

// newRLE32Decoder creates a new RLE decoder with bit-width w
func newRLE32Decoder(w int) *rle32Decoder {
	if w <= 0 || w > 32 {
		panic("invalid width value")
	}
	d := rle32Decoder{
		bitWidth:   w,
		byteWidth:  (w + 7) / 8,
		bpUnpacker: unpack8Int32FuncForWidth(w),
	}
	return &d
}

func (d *rle32Decoder) init(data []byte, count int) {
	d.data = data
	d.pos = 0
	d.i = 0
	d.count = count
}

func (d *rle32Decoder) decode(levels []int) (n int, err error) {
	n = len(levels)
	if d.count-d.i < n {
		n = d.count - d.i
	}
	for i := 0; i < n; i++ {
		k, err := d.next()
		if err != nil {
			return i, err
		}
		d.i++
		levels[i] = int(k)
	}
	return n, nil
}

func (d *rle32Decoder) next() (next int32, err error) {
	if d.rleCount == 0 && d.bpCount == 0 && d.bpRunPos == 0 {
		if err = d.readRunHeader(); err != nil {
			return
		}
	}

	if d.rleCount > 0 {
		next = d.rleValue
		d.rleCount--
	} else if d.bpCount > 0 || d.bpRunPos > 0 {
		if d.bpRunPos == 0 {
			if err = d.readBitPackedRun(); err != nil {
				return
			}
			d.bpCount--
		}
		next = d.bpRun[d.bpRunPos]
		d.bpRunPos = (d.bpRunPos + 1) % 8
	} else {
		panic("should not happen")
	}

	return
}

func (d *rle32Decoder) readRLERunValue() error {
	n := d.pos + d.byteWidth // TODO: overflow?
	if n > len(d.data) {
		return fmt.Errorf("rle: cannot read run value (not enough data)")
	}
	d.rleValue = unpackLittleEndianInt32(d.data[d.pos:n])
	d.pos = n
	return nil
}

func (d *rle32Decoder) readBitPackedRun() error {
	n := d.pos + d.bitWidth
	if n > len(d.data) {
		return fmt.Errorf("rle: cannot read bit-packed run (not enough data)")
	}
	// TODO: remember unpack func in d
	d.bpRun = d.bpUnpacker(d.data[d.pos:n])
	d.pos = n
	return nil
}

func (d *rle32Decoder) readRunHeader() error {
	if d.pos >= len(d.data) {
		return fmt.Errorf("rle: no more data")
	}

	h, n := binary.Uvarint(d.data[d.pos:])
	if n <= 0 || h > math.MaxUint32 { // TODO: maxUint32 or maxInt32?
		// TODO: better errror mesage
		return fmt.Errorf("rle: failed to read run header (Uvarint result: %d, %d)", h, n)
	}
	d.pos += n
	if h&1 == 1 {
		d.bpCount = uint32(h >> 1)
		if d.bpCount == 0 {
			return fmt.Errorf("rle: empty bit-packed run")
		}
		d.bpRunPos = 0
	} else {
		d.rleCount = uint32(h >> 1)
		if d.rleCount == 0 {
			return fmt.Errorf("rle: empty RLE run")
		}
		return d.readRLERunValue()
	}
	return nil
}
