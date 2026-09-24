# Regenerates this folder's Parquet fixtures (see README.md). Run from here:
#   pip install pyarrow==25.0.1 && python3 gen.py ../../../../.captures/external
# The rows are real: Arena leaderboard rows the dataset viewer served on
# 2026-09-24 (scripts/verify.command's captures). The files are written the
# way Arena's own are (parquet-cpp-arrow; Snappy; dictionary pages; data
# page v1; optional columns; row groups of 1000), plus variants that each
# exercise one more thing the reader claims to take.
import glob, json, os, sys
import pyarrow as pa, pyarrow.parquet as pq

cap = sys.argv[1]
rows = []
for f in sorted(glob.glob(os.path.join(cap, 'datasets-server.huggingface.co_filter_config_text_*'))):
    rows += [r['row'] for r in json.load(open(f))['rows']]
seen, uniq = set(), []
for r in rows:
    k = (r['model_name'], r['category'])
    if k not in seen:
        seen.add(k); uniq.append(r)
cols = ['model_name', 'organization', 'license', 'rating', 'rating_lower', 'rating_upper',
        'variance', 'vote_count', 'rank', 'category', 'leaderboard_publish_date']
schema = pa.schema([(c, pa.float64() if c in ('rating', 'rating_lower', 'rating_upper', 'variance', 'vote_count', 'rank') else pa.string()) for c in cols])
table = pa.Table.from_pylist([{c: r.get(c) for c in cols} for r in uniq], schema=schema)
print(len(uniq), 'rows')

# Arena's own settings.
pq.write_table(table, 'arena-text.parquet', compression='snappy', row_group_size=1000, data_page_version='1.0')

# Variants: data page v2; no compression; plain encoding (no dictionary);
# nulls and an int64 column; small pages (several per chunk).
small = table.slice(0, 300)
pq.write_table(small, 'v2.parquet', compression='snappy', data_page_version='2.0', row_group_size=128)
pq.write_table(small, 'uncompressed.parquet', compression='none', row_group_size=128)
pq.write_table(small, 'plain.parquet', compression='snappy', use_dictionary=False, row_group_size=128)
mixed = pa.table({
    'name': pa.array(['a', None, 'c', 'dd', None, 'f'] * 50, pa.string()),
    'n': pa.array([1, 2, None, -4, 5, 2**40] * 50, pa.int64()),
    'i': pa.array([7, None, -9, 10, 11, 12] * 50, pa.int32()),
    'x': pa.array([0.5, None, 1.25, -2.0, None, 3.0] * 50, pa.float64()),
})
pq.write_table(mixed, 'nulls.parquet', compression='snappy', data_page_size=256, row_group_size=200)
pq.write_table(mixed, 'nulls-v2.parquet', compression='snappy', data_page_size=256, row_group_size=200, data_page_version='2.0')

# What the Go test compares against: pyarrow's own reading of each file.
# (v2, uncompressed and plain hold the same rows as arena-text: the test
# compares them with it; nulls-v2 holds nulls' rows.)
out = {}
for f in ['arena-text.parquet', 'nulls.parquet']:
    t = pq.read_table(f)
    out[f] = {'rows': t.num_rows, 'columns': t.column_names, 'values': t.to_pylist()}
json.dump(out, open('expected.json', 'w'), separators=(',', ':'))
