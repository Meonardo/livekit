// Copyright 2023 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package utils

import (
	"errors"
	"math"
	"unsafe"
)

const (
	minRanges = 1
)

var (
	errReversedOrder = errors.New("end <= start")
	errKeyNotFound   = errors.New("key not found")
	errKeyTooOld     = errors.New("key too old")
	errKeyExcluded   = errors.New("key excluded")
)

type rangeType interface {
	uint32 | uint64
}

type valueType interface {
	uint32 | uint64
}

type rangeVal[RT rangeType, VT valueType] struct {
	start RT
	end   RT
	value VT
}

type RangeMap[RT rangeType, VT valueType] struct {
	halfRange RT

	size   int
	ranges []rangeVal[RT, VT]
}

func NewRangeMap[RT rangeType, VT valueType](size int) *RangeMap[RT, VT] {
	var t RT
	r := &RangeMap[RT, VT]{
		halfRange: 1 << ((unsafe.Sizeof(t) * 8) - 1),
		size:      int(math.Max(float64(size), float64(minRanges))),
	}
	r.initRanges(0)
	return r
}

func (r *RangeMap[RT, VT]) ClearAndResetValue(val VT) {
	r.initRanges(val)
}

func (r *RangeMap[RT, VT]) DecValue(dec VT) {
	r.ranges[len(r.ranges)-1].value -= dec
}

func (r *RangeMap[RT, VT]) initRanges(val VT) {
	r.ranges = []rangeVal[RT, VT]{
		{
			start: 0,
			end:   0,
			value: val,
		},
	}
}

func (r *RangeMap[RT, VT]) ExcludeRange(startInclusive RT, endExclusive RT) error {
	if endExclusive == startInclusive || endExclusive-startInclusive > r.halfRange {
		return errReversedOrder
	}

	lr := &r.ranges[len(r.ranges)-1]
	if lr.start > startInclusive {
		// start of open range is after start of exclusion range, cannot close the open range
		return errReversedOrder
	}

	newValue := lr.value + VT(endExclusive-startInclusive)

	// if start of exclusion range matches start of open range, move the open range
	if lr.start == startInclusive {
		lr.start = endExclusive
		lr.value = newValue
		return nil
	}

	// close previous range
	lr.end = startInclusive - 1

	// start new open one after given exclusion range
	r.ranges = append(r.ranges, rangeVal[RT, VT]{
		start: endExclusive,
		end:   0,
		value: newValue,
	})

	r.prune()
	return nil
}

func (r *RangeMap[RT, VT]) GetValue(key RT) (VT, error) {
	numRanges := len(r.ranges)
	if numRanges != 0 {
		if key >= r.ranges[numRanges-1].start {
			// in the open range
			return r.ranges[numRanges-1].value, nil
		}

		if key < r.ranges[0].start {
			// too old
			return 0, errKeyTooOld
		}
	}

	for idx := numRanges - 1; idx >= 0; idx-- {
		rv := &r.ranges[idx]
		if idx != numRanges-1 {
			// open range checked above
			if key-rv.start < r.halfRange && rv.end-key < r.halfRange {
				return rv.value, nil
			}
		}

		if idx > 0 {
			rvPrev := &r.ranges[idx-1]
			beforeDiff := key - rvPrev.end
			afterDiff := rv.start - key
			if beforeDiff > 0 && beforeDiff < r.halfRange && afterDiff > 0 && afterDiff < r.halfRange {
				// in excluded range
				return 0, errKeyExcluded
			}
		}
	}

	return 0, errKeyNotFound
}

func (r *RangeMap[RT, VT]) prune() {
	if len(r.ranges) > r.size+1 { // +1 to accommodate the open range
		r.ranges = r.ranges[len(r.ranges)-r.size-1:]
	}
}
