# order testdata

## order_fixture.json

Frozen cross-check data for `TestCrossCheckAgainstReference` and
`TestByteOrderEqualsRankOrderAndRoundTrip`. It is the output of the reference
implementation this package ports — [rocicorp/fractional-indexing][ref] (CC0) —
run over a deterministic sequence of `generateKeyBetween` operations. The Go
test replays the identical sequence through this package's `KeyBetween` and
asserts byte-for-byte equality, so any divergence from the reference fails.

Shape:

- `produced`: each step's `{step, op, key}` where `op` is 0 append, 1 prepend,
  2 insert-between.
- `finalSorted`: the 400 keys in ascending (rank = byte-wise) order.

### Regenerating

The sequence is driven by an exact integer LCG (BigInt, so it matches Go's
int64 arithmetic; plain JS `number` loses precision past 2^53 and would diverge):

```
x0 = 42
x   = (x * 1103515245 + 12345) mod 2^31   // rnd() returns the new x
```

At each of 400 steps, with `keys` the running sorted list:

- 0 keys -> `op = 0` (append).
- 1 key  -> `op = rnd() mod 2`.
- 2+ keys -> `op = rnd() mod 3`; for `op == 2` the insert index is
  `rnd() mod (len(keys) - 1)`.

Append is `generateKeyBetween(last, null)`, prepend `generateKeyBetween(null,
first)`, between `generateKeyBetween(keys[i], keys[i+1])`. Emit `{produced,
finalSorted}` as compact JSON. `TestCrossCheckAgainstReference` mirrors this
selection logic exactly; keep the two in lock-step if either changes.

[ref]: https://github.com/rocicorp/fractional-indexing
