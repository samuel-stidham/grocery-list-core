# fdc HTTP API

Base URL on the production daemon: `http://127.0.0.1:8471`. The test instance
started by `make run` listens on `http://127.0.0.1:8474`. Every path is under
`/v1`. Every response is JSON and carries the header
`X-Data-Attribution: U.S. Department of Agriculture, Agricultural Research Service. FoodData Central. https://fdc.nal.usda.gov`,
including errors. The database is read-only, so there are no writes. A
running server serves the release it opened. After an import, restart it,
and `/v1/about` reports the new `data_version` and `imported_at`.

Nutrient amounts are per 100 g of food, in the unit USDA assigns to the
nutrient. Grams are the only quantity the API understands. Converting a cup
or a slice to grams is the caller's job, using `/v1/foods/{fdc_id}/portions`.

## Errors

Every error has the same body and a matching HTTP status.

```json
{"error": {"code": "not_found", "message": "fdc_id 1 not found"}}
```

| code | status | when |
| --- | --- | --- |
| `bad_request` | 400 | a query parameter, path value, or body failed validation |
| `bad_request` | 413 | a score body over 1 MiB |
| `not_found` | 404 | unknown `fdc_id`, or no such route |
| `method_not_allowed` | 405 | the route exists for another method, `Allow` names it |
| `forbidden` | 403 | the `Host` header names something other than this loopback server |
| `internal` | 500 | a database error, which should not happen on a verified file |

A 500 body always says `internal error` and nothing more. The cause is in the
server log as a `request failed` record with the route. A 400 for a malformed
score body names the JSON problem in the caller's terms, such as
`body: field "servings" has the wrong type`, never a Go type. Every response
carries `Content-Type: application/json; charset=utf-8` and
`X-Content-Type-Options: nosniff`.

Requests must address the server as `127.0.0.1`, `localhost`, or `::1` on any
port, or as the host given to `--listen`. Any other `Host` is a 403. A browser
tricked by DNS rebinding sends the attacker's name and is refused. The data is
public, so this costs nothing and closes a door.

## Food types

The `type` parameter and the `data_type` field use short names.

| short name | USDA `data_type` |
| --- | --- |
| `sr_legacy` | `sr_legacy_food` |
| `foundation` | `foundation_food` |
| `branded` | `branded_food` |
| `survey` | `survey_fndds_food` |

## The eight nutrients every response carries

| id | name | unit |
| --- | --- | --- |
| 1008 | Energy | KCAL |
| 1003 | Protein | G |
| 1005 | Carbohydrate, by difference | G |
| 2000 | Total Sugars | G |
| 1079 | Fiber, total dietary | G |
| 1087 | Calcium, Ca | MG |
| 1089 | Iron, Fe | MG |
| 1093 | Sodium, Na | MG |

These keys are always present in a food's `nutrients` map and in both maps of
a score. A food with no value has `"amount": null`. Energy is special. Most
Foundation foods have no 1008 row and only carry the Atwater energies. When
1008 is missing, the value of 2048 (Atwater Specific Factors) is used, then
2047 (Atwater General Factors). The response says so in `derived_from`.

## GET /v1/about

Version and data provenance.

```sh
curl -s http://127.0.0.1:8471/v1/about
```

```json
{
  "version": "v0.1.0",
  "data_version": "2026-04-30",
  "data_source_url": "https://fdc.nal.usda.gov/fdc-datasets/FoodData_Central_csv_2026-04-30.zip",
  "data_sha256": "c33d91e5c44ddbcd71ed6038714fb648b26089b3f91fabfa3be63d96d5763a01",
  "imported_at": "2026-10-27T01:02:03Z",
  "importer_version": "v0.1.0",
  "types": ["sr_legacy", "foundation", "branded", "survey"],
  "food_count": 2013644,
  "food_count_by_type": {"branded": 1999950, "foundation": 469, "sr_legacy": 7793, "survey": 5432},
  "attribution": "U.S. Department of Agriculture, Agricultural Research Service. FoodData Central. https://fdc.nal.usda.gov"
}
```

## GET /v1/foods?q=&type=&limit=

Full text search over descriptions and, for branded foods, brand owner and
brand name. Every word in `q` must match, and the last word matches as a
prefix, so `spin` finds `Spinach, raw`. Diacritics are ignored. A `q` of 8 to
14 digits is also looked up as a GTIN or UPC, with leading zeros tolerated.

Results put SR Legacy, Foundation, and Survey foods before Branded ones, then
rank by relevance within each group. On the full database a common word such
as `milk` answers in about 150 ms, and a single letter takes over a second. A
client should wait for two characters and debounce typing.

| parameter | required | meaning |
| --- | --- | --- |
| `q` | yes | the search text, at most 200 characters |
| `type` | no | comma separated short names to keep, default all imported types |
| `limit` | no | 1 to 200, default 25, larger values are capped |

```sh
curl -s 'http://127.0.0.1:8471/v1/foods?q=spinach%20raw&type=sr_legacy,foundation&limit=5'
```

```json
{
  "items": [
    {
      "fdc_id": 168462,
      "description": "Spinach, raw",
      "data_type": "sr_legacy",
      "brand": null,
      "category": "Vegetables and Vegetable Products"
    }
  ],
  "limit": 5
}
```

`brand` is an object for branded foods and `null` otherwise.

```json
{"brand": {"owner": "Richardson Oilseed Products (US) Limited", "name": null, "gtin_upc": "00027000612323"}}
```

`category` is the USDA food category for SR Legacy and Foundation foods. For
Survey foods it is the WWEIA category, and for Branded foods the branded
category. It is `null` when USDA assigned none.

## GET /v1/foods/{fdc_id}

One food with every nutrient USDA reports for it, per 100 g.

```sh
curl -s http://127.0.0.1:8471/v1/foods/168462
```

```json
{
  "fdc_id": 168462,
  "description": "Spinach, raw",
  "data_type": "sr_legacy",
  "brand": null,
  "category": "Vegetables and Vegetable Products",
  "per_grams": 100,
  "nutrients": {
    "1008": {"name": "Energy", "unit": "KCAL", "amount": 23, "derived_from": null},
    "1089": {"name": "Iron, Fe", "unit": "MG", "amount": 2.71, "derived_from": null},
    "1093": {"name": "Sodium, Na", "unit": "MG", "amount": 79, "derived_from": null}
  }
}
```

The real response holds every nutrient the food has. Keys are nutrient ids as
strings. A Foundation food without a 1008 row looks like this for energy:

```json
{"1008": {"name": "Energy", "unit": "KCAL", "amount": 150, "derived_from": 2048}}
```

When USDA shipped the same nutrient twice for one food, the row with the
lower id is the one reported. `/v1/score` reads the same row, so a lower row
with no amount makes the nutrient missing on both endpoints.

## GET /v1/foods/{fdc_id}/portions

Household measures with their gram weights, the input for unit conversion.

```sh
curl -s http://127.0.0.1:8471/v1/foods/168462/portions
```

```json
{
  "fdc_id": 168462,
  "items": [
    {"id": 83132, "seq_num": 1, "amount": 1, "unit": null, "label": "1 cup", "description": null, "modifier": "cup", "gram_weight": 30},
    {"id": 83133, "seq_num": 2, "amount": 1, "unit": null, "label": "1 bunch", "description": null, "modifier": "bunch", "gram_weight": 340}
  ]
}
```

`label` is the text to show. It is composed per data type, because USDA
stores the measure in different columns for each type. SR Legacy puts it in
`modifier`. Foundation uses a real `unit` plus `modifier`, with `description`
holding a comment. Survey writes the whole measure, amount included, in
`description`, which is why `amount` and `unit` are `null` for those rows.

Rows without a usable gram weight are left out. Survey foods carry a
"Quantity not specified" row with a weight of zero, and it is dropped.

Branded foods have no portion rows in USDA's data. Their label serving is
returned as one item instead. A serving in grams has `gram_weight`. A serving
in millilitres keeps `"unit": "ml"` and `"gram_weight": null`, because volume
is not weight and the API will not guess a density. USDA spells grams as `g`,
`GRM`, and `GM`, and millilitres as `ml` and `MLT`. The API normalizes those to
`g` and `ml`, so a client matches on two strings. Other units (`MG`, `IU`,
`MC`) come back lowercased with no gram weight. When USDA has no household
text, `label` is the size and unit, such as `355 ml`.

```json
{
  "fdc_id": 1105904,
  "items": [
    {"id": null, "seq_num": 1, "amount": 15, "unit": "ml", "label": "1 Tbsp (15 ml)", "description": null, "modifier": null, "gram_weight": null}
  ]
}
```

## POST /v1/score

Sum the eight nutrients over a list of foods and gram amounts, then divide by
servings.

Request:

```sh
curl -s -X POST http://127.0.0.1:8471/v1/score \
  -H 'Content-Type: application/json' \
  -d '{"servings": 4, "items": [{"fdc_id": 168462, "grams": 300}, {"fdc_id": 1105904, "grams": 10}]}'
```

| field | rule |
| --- | --- |
| `servings` | 0.001 to 1,000,000, fractions allowed |
| `items` | 1 to 500 entries |
| `items[].fdc_id` | a positive integer that exists, else 400 naming every unknown id |
| `items[].grams` | 0 to 1,000,000 |

The body is one JSON object of at most 1 MiB. Unknown fields are rejected, and
so is anything after the closing brace. The bounds are guards against overflow
and nonsense, not nutrition rules. A million grams is a tonne.

Response:

```json
{
  "servings": 4,
  "total_grams": 310,
  "per_serving": {
    "1008": {"name": "Energy", "unit": "KCAL", "amount": 39.35},
    "1089": {"name": "Iron, Fe", "unit": "MG", "amount": 2.0325}
  },
  "total": {
    "1008": {"name": "Energy", "unit": "KCAL", "amount": 157.4},
    "1089": {"name": "Iron, Fe", "unit": "MG", "amount": 8.13}
  },
  "missing": [{"fdc_id": 1105904, "nutrient_id": 1003}]
}
```

Both maps always hold all eight keys. A food with no value for a nutrient
contributes zero to that nutrient and is listed in `missing`. A caller can then
mark the figure as incomplete. The energy fallback applies per food, the same
way it does in `/v1/foods/{fdc_id}`.