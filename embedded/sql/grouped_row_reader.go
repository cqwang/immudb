/*
Copyright 2021 CodeNotary, Inc. All rights reserved.

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
package sql

import "github.com/codenotary/immudb/embedded/store"

type groupedRowReader struct {
	e *Engine

	rowReader RowReader

	selectors []Selector

	groupBy []*ColSelector

	currRow *Row
}

func (e *Engine) newGroupedRowReader(rowReader RowReader, selectors []Selector, groupBy []*ColSelector) (*groupedRowReader, error) {
	return &groupedRowReader{
		e:         e,
		rowReader: rowReader,
		selectors: selectors,
		groupBy:   groupBy,
	}, nil
}

func (gr *groupedRowReader) ImplicitDB() string {
	return gr.rowReader.ImplicitDB()
}

func (gr *groupedRowReader) ImplicitTable() string {
	return gr.rowReader.ImplicitTable()
}

func (gr *groupedRowReader) Columns() ([]*ColDescriptor, error) {
	colsBySel, err := gr.colsBySelector()
	if err != nil {
		return nil, err
	}

	colsByPos := make([]*ColDescriptor, len(gr.selectors))

	for i, sel := range gr.selectors {
		encSel := EncodeSelector(sel.resolve(gr.rowReader.ImplicitDB(), gr.rowReader.ImplicitTable()))
		colsByPos[i] = colsBySel[encSel]
	}

	return colsByPos, nil
}

func (gr *groupedRowReader) colsBySelector() (map[string]*ColDescriptor, error) {
	colDescriptors, err := gr.rowReader.colsBySelector()
	if err != nil {
		return nil, err
	}

	for _, sel := range gr.selectors {
		aggFn, db, table, col := sel.resolve(gr.rowReader.ImplicitDB(), gr.rowReader.ImplicitTable())

		if aggFn == "" {
			continue
		}

		encSel := EncodeSelector(aggFn, db, table, col)

		if aggFn == COUNT {
			colDescriptors[encSel] = &ColDescriptor{Selector: encSel, Type: IntegerType}
			continue
		}

		colDesc, ok := colDescriptors[EncodeSelector("", db, table, col)]
		if !ok {
			return nil, ErrColumnDoesNotExist
		}

		if aggFn == MAX || aggFn == MIN {
			colDescriptors[encSel] = colDesc
		} else {
			// SUM, AVG
			colDescriptors[encSel] = &ColDescriptor{Selector: encSel, Type: IntegerType}
		}
	}

	return colDescriptors, nil
}

func (gr *groupedRowReader) Read() (*Row, error) {
	for {
		row, err := gr.rowReader.Read()
		if err == store.ErrNoMoreEntries {
			if gr.currRow == nil {
				return nil, err
			}

			r := gr.currRow
			gr.currRow = nil
			return r, nil
		}
		if err != nil {
			return nil, err
		}

		if gr.currRow == nil {
			gr.currRow = row
			err = gr.initAggregations()
			if err != nil {
				return nil, err
			}
			continue
		}

		compatible, err := gr.currRow.Compatible(row, gr.groupBy, gr.rowReader.ImplicitDB(), gr.rowReader.ImplicitTable())
		if err != nil {
			return nil, err
		}

		if !compatible {
			r := gr.currRow
			gr.currRow = row
			err = gr.initAggregations()
			if err != nil {
				return nil, err
			}
			return r, nil
		}

		// Compatible rows get merged
		for _, v := range gr.currRow.Values {
			aggV, isAggregatedValue := v.(AggregatedValue)

			if isAggregatedValue {
				if aggV.ColBounded() {
					val, exists := row.Values[aggV.Selector()]
					if !exists {
						return nil, ErrColumnDoesNotExist
					}

					err = aggV.updateWith(val)
					if err != nil {
						return nil, err
					}
				}

				if !aggV.ColBounded() {
					err = aggV.updateWith(nil)
					if err != nil {
						return nil, err
					}
				}
			}
		}
	}
}

func (gr *groupedRowReader) initAggregations() error {
	// augment row with aggregated values
	for _, sel := range gr.selectors {
		aggFn, db, table, col := sel.resolve(gr.rowReader.ImplicitDB(), gr.rowReader.ImplicitTable())

		encSel := EncodeSelector(aggFn, db, table, col)

		switch aggFn {
		case COUNT:
			{
				if col != "*" {
					return ErrLimitedCount
				}

				gr.currRow.Values[encSel] = &CountValue{sel: EncodeSelector("", db, table, col)}
			}
		case SUM:
			{
				gr.currRow.Values[encSel] = &SumValue{sel: EncodeSelector("", db, table, col)}
			}
		case MIN:
			{
				gr.currRow.Values[encSel] = &MinValue{sel: EncodeSelector("", db, table, col)}
			}
		case MAX:
			{
				gr.currRow.Values[encSel] = &MaxValue{sel: EncodeSelector("", db, table, col)}
			}
		case AVG:
			{
				gr.currRow.Values[encSel] = &AVGValue{sel: EncodeSelector("", db, table, col)}
			}
		}
	}

	for _, v := range gr.currRow.Values {
		aggV, isAggregatedValue := v.(AggregatedValue)

		if isAggregatedValue {
			if aggV.ColBounded() {
				val, exists := gr.currRow.Values[aggV.Selector()]
				if !exists {
					return ErrColumnDoesNotExist
				}

				err := aggV.updateWith(val)
				if err != nil {
					return err
				}
			}

			if !aggV.ColBounded() {
				err := aggV.updateWith(nil)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (gr *groupedRowReader) Close() error {
	return gr.rowReader.Close()
}
