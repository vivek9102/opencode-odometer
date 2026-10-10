# Pricing and accounting

Odometer receives token counts from OpenCode and records an accounting source
for each message. Local pricing supports providers that do not report monetary
costs, as well as gateways whose rates differ from public catalog rates.

All token rates are **USD per one million tokens**. Displayed costs are accounting
estimates unless they come from a provider-reported amount or a confirmed local
rate. Use your provider's invoice to validate billing.

## Cost-source precedence

For each message, Odometer uses the first applicable source:

1. An explicit free classification or saved local rate.
2. A non-zero cost reported through OpenCode.
3. An exact public catalog rate.
4. A deterministic cross-provider price estimate.
5. An unpriced zero amount, flagged for review.

A zero cost reported by OpenCode does not establish that the route is free.
An unknown amount can understate spending and budget usage. Use **REVIEW PRICES**
to confirm rates before relying on a spending cap.

Ordinary public refreshes, saved rate changes and resets apply to future messages;
they do not reprice completed message history. A provider/model switch does not
erase accounting history.

## Price files and model keys

| File | Purpose | Catalog refresh |
|---|---|---|
| `prices.json` | Cached public catalog | Replaced with refreshed public rates |
| `prices.local.json` | Private or custom rates | Preserved |

Keys use `providerID/modelID`, matching the route recorded by OpenCode. The same
model name can have different rates under different providers. An unmatched
private route may receive a labelled cross-provider estimate. If no usable
price or reported cost exists, it remains unpriced.

Example format, using illustrative rates:

```json
{
  "reference_model": "example/paid-model",
  "models": {
    "example/paid-model": {
      "name": "Example paid model",
      "input": 2.5,
      "output": 10.0,
      "cache_read": 0.25,
      "cache_write": 3.125
    }
  }
}
```

| Field | Meaning |
|---|---|
| `input` | Rate for ordinary input tokens |
| `output` | Rate for generated output tokens |
| `cache_read` | Rate for input served from cache |
| `cache_write` | Rate for tokens written to cache |
| `free` | Explicit free classification |
| `name` | Display label; does not change route identity |

Omitted numeric fields default to zero. Set cache rates according to your
provider's billing rather than assuming a fixed discount or markup. Different
cache categories are accounted separately.

## Updating public prices

**UPDATE CATALOG** downloads public pricing from [models.dev](https://models.dev).
Startup loads cached or embedded data before refreshing in the background, so
an unavailable network does not block the application. A refresh failure retains
usable cached pricing. The catalog-age tooltip shows when it was last updated.

Public refreshes preserve `prices.local.json`. Do not place private rates in
the public catalog file: they would be replaced by a later refresh.

## Custom and private rates

Open **REVIEW PRICES** to inspect unknown, estimated and saved custom routes.
Save the applicable input/output/cache rates, or explicitly mark the exact route
free. Saved rates can be revisited or reset. Ignoring an unknown route leaves its
cost uncounted; ignoring an estimated route leaves the estimate in use.

For a file-based overlay, copy the repository's
[example](../prices.local.example.json) into the application data directory:

```powershell
Copy-Item prices.local.example.json `
  "$env:LOCALAPPDATA\OpenCodeOdometer\prices.local.json"
```

Add exact route keys and the rates from your provider. For example:

```json
{
  "reference_model": "example/paid-model",
  "models": {
    "example/paid-model": {
      "name": "Example gateway model",
      "input": 2.75,
      "output": 11.0,
      "cache_read": 0.275,
      "cache_write": 3.4375
    },
    "example/free-model": {
      "name": "Example free route",
      "free": true,
      "input": 0,
      "output": 0,
      "cache_read": 0,
      "cache_write": 0
    }
  }
}
```

These are illustrative rates, not current provider prices. Overlay entries
replace matching catalog rates and add new keys; an overlay `reference_model`
also takes precedence. Restart Odometer after manual file edits to reload them.
The UI's save actions apply without a restart.

On Windows, the default data directory is `%LOCALAPPDATA%\OpenCodeOdometer`.
`OPENCODE_ODOMETER_DIR` overrides it. Use the application's discovery pointer
to confirm the effective directory. Keep private overlays outside the public
repository; the example contains no provider credentials.

## Free routes and savings

Exact local and catalog entries take precedence in rate lookup. When no exact
entry exists, naming conventions such as `-free` or `sovereign` can classify a
route as free before cross-provider estimation. Check your actual route's
billing and use an exact local override if that convention does not apply.
Unknown prices and zero provider-reported costs are not proof of free pricing.

Free usage contributes token counts and an estimated **SAVED (FREE)** amount.
The `reference_model` selects the rates used to value this amount. Set it to a
paid route that exists in your merged table. The reference resolves from the
overlay or public table, with a built-in fallback when needed.

Savings answer what the recorded usage would cost at reference rates. They are
not invoice discounts, provider charges or evidence that the reference model
would use the same number of tokens.

## Reports and invoice comparison

**USAGE → EXPORT PERIOD CSV** exports daily model/provider totals for the selected
dates, including pricing source and estimated/unknown coverage. The main
**EXPORT CSV** follows Open sessions, Since reset or All time. Archived summaries
are labelled rather than presented as full message details.

To compare with an invoice, choose the same dates and provider, then compare
cost totals and token categories. Consider timezone, rounding, provider-specific
fees, usage outside OpenCode and any unknown or estimated prices. The report
identifies older amounts whose dated detail is unavailable.

## Troubleshooting

| Symptom | Check |
|---|---|
| Unknown or estimated price | Confirm the exact provider/model key and save the applicable rates under REVIEW PRICES. |
| Costs differ from the invoice | Compare input, output and cache categories, billing dates, route rates and price-source coverage. |
| Savings are zero or misleading | Confirm `reference_model` exists and has appropriate paid rates. |
| Overlay edits have no effect | Check valid JSON, the effective data directory and whether Odometer has reloaded the file. |
| A route is incorrectly free | Save an exact local entry matching the provider/model key and its billed rates. |

For the relationship between pricing and enforcement, see
[session budgets](BUDGET_RULES.md#enforcement-and-validation-limits).
