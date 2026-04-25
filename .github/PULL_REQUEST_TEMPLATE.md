## Summary

<!-- One paragraph that the release notes would quote verbatim. -->

## Why

<!-- Linked issue, broken behaviour, or product motivation. -->

## How

<!-- 3–5 bullets explaining the implementation choices. -->

## Test plan

- [ ] `make lint` clean
- [ ] `make test` clean
- [ ] Manual reproduction (commands attached) for behavioural changes
- [ ] Migrations applied + rolled back successfully

## Risk

<!-- What could go wrong in production? Rollback plan? -->

## Checklist

- [ ] Doc comments updated for any public API change
- [ ] CHANGELOG entry (if user-visible)
- [ ] No secrets / debug logs / `fmt.Println` left behind
