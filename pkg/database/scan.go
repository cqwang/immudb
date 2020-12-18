/*
Copyright 2019-2020 vChain, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package database

import (
	"github.com/codenotary/immudb/embedded/store"
	"github.com/codenotary/immudb/embedded/tbtree"
	"github.com/codenotary/immudb/pkg/api/schema"
)

//Scan ...
func (d *db) Scan(req *schema.ScanRequest) (*schema.ItemList, error) {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	if req == nil {
		return nil, store.ErrIllegalArguments
	}

	if req.Limit > MaxKeyScanLimit {
		return nil, ErrMaxKeyScanLimitExceeded
	}

	limit := req.Limit

	if req.Limit == 0 {
		limit = MaxKeyScanLimit
	}

	var items []*schema.Item
	i := uint64(0)

	err := d.WaitForIndexingUpto(req.SinceTx)
	if err != nil {
		return nil, err
	}

	snap, err := d.st.SnapshotSince(req.SinceTx)
	if err != nil {
		return nil, err
	}
	defer snap.Close()

	r, err := d.st.NewReader(
		snap,
		&tbtree.ReaderSpec{
			SeekKey:   wrapWithPrefix(req.SeekKey, setKeyPrefix),
			Prefix:    wrapWithPrefix(req.Prefix, setKeyPrefix),
			DescOrder: req.Desc,
		})
	if err != nil {
		return nil, err
	}
	defer r.Close()

	for {
		key, _, tx, err := r.Read()
		if err == tbtree.ErrNoMoreEntries {
			break
		}
		if err != nil {
			return nil, err
		}

		item, err := d.getAt(key, tx, 0, snap, d.tx1)
		if err != nil {
			return nil, err
		}

		items = append(items, item)
		if i++; i == limit {
			break
		}
	}

	return &schema.ItemList{
		Items: items,
	}, nil
}
