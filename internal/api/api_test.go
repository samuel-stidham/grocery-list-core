package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samuel-stidham/grocery-list-core/internal/api"
	"github.com/samuel-stidham/grocery-list-core/internal/importer"
	"github.com/samuel-stidham/grocery-list-core/internal/logging"
	"github.com/samuel-stidham/grocery-list-core/internal/meta"
	"github.com/samuel-stidham/grocery-list-core/internal/store"
)

const (
	spinachID   = "168462"
	hummusID    = "321358"
	syntheticID = "9000001"
	oilID       = "1105904"
	soupID      = "1105906"
	tolerance   = 1e-9
)

//nolint:gochecknoglobals // one fixture server shared by every API test, started in TestMain
var srv *httptest.Server

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "api-test")
	if err != nil {
		panic(err)
	}

	out := filepath.Join(dir, "fdc.sqlite")

	_, err = importer.Run(context.Background(), importer.Options{Src: "../importer/testdata/fixture", Out: out})
	if err != nil {
		panic(err)
	}

	database, err := store.OpenReadOnly(context.Background(), out)
	if err != nil {
		panic(err)
	}

	server, err := api.New(context.Background(), database, api.Options{
		Version: "test-version",
		Logger:  logging.New(io.Discard, logging.Options{}),
	})
	if err != nil {
		panic(err)
	}

	srv = httptest.NewServer(server.Handler())
	code := m.Run()

	srv.Close()

	_ = database.Close()
	_ = os.RemoveAll(dir)

	os.Exit(code)
}

// reply is one decoded response. Every endpoint answers with a JSON object.
type reply struct {
	status int
	raw    []byte
	object map[string]any
}

// request sends one request to the fixture server, closes the body, checks
// the shared headers, and decodes the JSON object.
func request(t *testing.T, method, path, body string) reply {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}

	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("body is not a JSON object: %v\n%s", err, raw)
	}

	check(t, "Content-Type", resp.Header.Get("Content-Type"), "application/json; charset=utf-8")
	check(t, meta.AttributionHeader, resp.Header.Get(meta.AttributionHeader), meta.Attribution)

	return reply{status: resp.StatusCode, raw: raw, object: object}
}

func get(t *testing.T, path string) reply {
	t.Helper()

	return request(t, http.MethodGet, path, "")
}

func post(t *testing.T, path, body string) reply {
	t.Helper()

	return request(t, http.MethodPost, path, body)
}

// check compares two scalar values and reports a mismatch without stopping.
func check(t *testing.T, name string, got, want any) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v (%T), want %v (%T)", name, got, got, want, want)
	}
}

// checkNear compares two floats within the shared tolerance.
func checkNear(t *testing.T, name string, got any, want float64) {
	t.Helper()

	if number := asFloat(t, got); math.Abs(number-want) >= tolerance {
		t.Errorf("%s = %v, want %v", name, number, want)
	}
}

func asMap(t *testing.T, value any) map[string]any {
	t.Helper()

	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%v (%T) is not an object", value, value)
	}

	return object
}

func asList(t *testing.T, value any) []any {
	t.Helper()

	list, ok := value.([]any)
	if !ok {
		t.Fatalf("%v (%T) is not a list", value, value)
	}

	return list
}

func asFloat(t *testing.T, value any) float64 {
	t.Helper()

	number, ok := value.(float64)
	if !ok {
		t.Fatalf("%v (%T) is not a number", value, value)
	}

	return number
}

func asString(t *testing.T, value any) string {
	t.Helper()

	text, ok := value.(string)
	if !ok {
		t.Fatalf("%v (%T) is not a string", value, value)
	}

	return text
}

func errorCode(t *testing.T, got reply) string {
	t.Helper()

	return asString(t, asMap(t, got.object["error"])["code"])
}

func errorMessage(t *testing.T, got reply) string {
	t.Helper()

	return asString(t, asMap(t, got.object["error"])["message"])
}

func items(t *testing.T, got reply) []any {
	t.Helper()

	return asList(t, got.object["items"])
}

func item(t *testing.T, got reply, index int) map[string]any {
	t.Helper()

	list := items(t, got)
	if index >= len(list) {
		t.Fatalf("no item %d in %v", index, got.object)
	}

	return asMap(t, list[index])
}

func nutrient(t *testing.T, got reply, nutrientID string) map[string]any {
	t.Helper()

	value, ok := asMap(t, got.object["nutrients"])[nutrientID]
	if !ok {
		t.Fatalf("nutrient %s missing from %v", nutrientID, got.object["nutrients"])
	}

	return asMap(t, value)
}

// amount digs total["<id>"].amount or per_serving["<id>"].amount out of a score reply.
func amount(t *testing.T, got reply, section, nutrientID string) any {
	t.Helper()

	return asMap(t, asMap(t, got.object[section])[nutrientID])["amount"]
}

// expectError checks the status and the error code of a failed call.
func expectError(t *testing.T, name string, got reply, status int, code string) {
	t.Helper()

	check(t, name+" status", got.status, status)
	check(t, name+" code", errorCode(t, got), code)
}

func TestAbout(t *testing.T) {
	got := get(t, "/v1/about")

	check(t, "status", got.status, http.StatusOK)
	check(t, "version", got.object["version"], "test-version")
	check(t, "data_version", got.object["data_version"], meta.DataVersion)
	check(t, "attribution", got.object["attribution"], meta.Attribution)
	check(t, "data_sha256", got.object["data_sha256"], meta.DataSHA256)
	check(t, "food_count", got.object["food_count"], 10.0)
	check(t, "branded count", asMap(t, got.object["food_count_by_type"])["branded"], 4.0)

	types := asList(t, got.object["types"])
	check(t, "types length", len(types), 4)
	check(t, "first type", types[0], "sr_legacy")

	if asString(t, got.object["imported_at"]) == "" {
		t.Errorf("imported_at is empty: %v", got.object)
	}
}

func TestUnknownRouteAndMethod(t *testing.T) {
	for _, path := range []string{"/v1/nothing", "/"} {
		expectError(t, path, get(t, path), http.StatusNotFound, "not_found")
	}

	deleted := request(t, http.MethodDelete, "/v1/about", "")
	expectError(t, "DELETE /v1/about", deleted, http.StatusMethodNotAllowed, "method_not_allowed")

	expectError(t, "GET /v1/score", get(t, "/v1/score"), http.StatusMethodNotAllowed, "method_not_allowed")
}

func TestSearchBasic(t *testing.T) {
	got := get(t, "/v1/foods?q=spin")

	check(t, "status", got.status, http.StatusOK)
	check(t, "hits", len(items(t, got)), 1)
	check(t, "limit", got.object["limit"], 25.0)

	hit := item(t, got, 0)
	check(t, "fdc_id", hit["fdc_id"], 168462.0)
	check(t, "data_type", hit["data_type"], "sr_legacy")
	check(t, "brand", hit["brand"], nil)
	check(t, "category", hit["category"], "Vegetables and Vegetable Products")

	check(t, "diacritics hits", len(items(t, get(t, "/v1/foods?q=cr%C3%A8me"))), 1)
	check(t, "no hits", len(items(t, get(t, "/v1/foods?q=zzzzzz"))), 0)

	got = get(t, "/v1/foods?q=12345678905")
	check(t, "gtin hits", len(items(t, got)), 1)
	check(t, "gtin", asMap(t, item(t, got, 0)["brand"])["gtin_upc"], "00012345678905")
}

func TestSearchFiltersAndLimit(t *testing.T) {
	check(t, "t prefix hits Testfood and TESTBRAND", len(items(t, get(t, "/v1/foods?q=t"))), 2)

	got := get(t, "/v1/foods?q=t&type=branded")
	check(t, "branded hits", len(items(t, got)), 1)
	check(t, "branded data_type", item(t, got, 0)["data_type"], "branded")

	got = get(t, "/v1/foods?q=t&limit=1")
	check(t, "limited hits", len(items(t, got)), 1)
	check(t, "limit echoed", got.object["limit"], 1.0)

	check(t, "limit capped at 200", get(t, "/v1/foods?q=t&limit=9999").object["limit"], 200.0)
}

func TestSearchValidation(t *testing.T) {
	cases := []string{
		"/v1/foods", "/v1/foods?q=", "/v1/foods?q=a&type=experimental",
		"/v1/foods?q=a&limit=0", "/v1/foods?q=a&limit=ten",
	}
	for _, path := range cases {
		expectError(t, path, get(t, path), http.StatusBadRequest, "bad_request")
	}
}

func TestFoodDetail(t *testing.T) {
	got := get(t, "/v1/foods/"+spinachID)

	check(t, "status", got.status, http.StatusOK)
	check(t, "description", got.object["description"], "Spinach, raw")
	check(t, "per_grams", got.object["per_grams"], 100.0)
	check(t, "data_type", got.object["data_type"], "sr_legacy")

	iron := nutrient(t, got, "1089")
	check(t, "iron amount", iron["amount"], 2.71)
	check(t, "iron unit", iron["unit"], "MG")
	check(t, "iron name", iron["name"], "Iron, Fe")
	check(t, "iron derived_from", iron["derived_from"], nil)

	check(t, "duplicate pair resolves to the lowest id", nutrient(t, got, "1087")["amount"], 99.0)
	check(t, "every nutrient the food has is present", nutrient(t, got, "1062")["unit"], "kJ")

	for _, nutrientID := range []string{"1008", "1003", "1005", "2000", "1079", "1087", "1089", "1093"} {
		nutrient(t, got, nutrientID)
	}
}

func TestFoodMinimumKeysAndEnergyFallback(t *testing.T) {
	got := get(t, "/v1/foods/"+syntheticID)

	energy := nutrient(t, got, "1008")
	check(t, "energy amount", energy["amount"], 150.0)
	check(t, "energy derived_from", energy["derived_from"], 2048.0)
	check(t, "energy name", energy["name"], "Energy")
	check(t, "energy unit", energy["unit"], "KCAL")

	sodium := nutrient(t, got, "1093")
	check(t, "missing sodium amount", sodium["amount"], nil)
	check(t, "missing sodium name", sodium["name"], "Sodium, Na")
	check(t, "missing sodium unit", sodium["unit"], "MG")
	check(t, "missing sodium derived_from", sodium["derived_from"], nil)

	// Hummus has 1008 and both Atwater ids: 1008 wins and nothing is derived.
	hummus := nutrient(t, get(t, "/v1/foods/"+hummusID), "1008")
	check(t, "hummus energy", hummus["amount"], 229.0)
	check(t, "hummus derived_from", hummus["derived_from"], nil)

	// Branded oil has no protein row.
	got = get(t, "/v1/foods/"+oilID)
	check(t, "oil protein", nutrient(t, got, "1003")["amount"], nil)
	check(t, "oil gtin", asMap(t, got.object["brand"])["gtin_upc"], "00027000612323")
}

func TestFoodErrors(t *testing.T) {
	got := get(t, "/v1/foods/1")
	expectError(t, "unknown id", got, http.StatusNotFound, "not_found")

	if !strings.Contains(errorMessage(t, got), "1") {
		t.Errorf("message must name the id: %v", got.object)
	}

	for _, path := range []string{"/v1/foods/abc", "/v1/foods/0", "/v1/foods/-5"} {
		expectError(t, path, get(t, path), http.StatusBadRequest, "bad_request")
	}

	expectError(t, "dropped sampling food", get(t, "/v1/foods/9000002"), http.StatusNotFound, "not_found")
}

func TestPortionsSRLegacyAndFoundation(t *testing.T) {
	got := get(t, "/v1/foods/"+spinachID+"/portions")
	check(t, "spinach portions", len(items(t, got)), 4)

	first := item(t, got, 0)
	check(t, "spinach label", first["label"], "1 cup")
	check(t, "spinach unit", first["unit"], nil)
	check(t, "spinach gram_weight", first["gram_weight"], 30.0)
	check(t, "spinach amount", first["amount"], 1.0)
	check(t, "spinach id", first["id"], 83132.0)

	hummus := item(t, get(t, "/v1/foods/"+hummusID+"/portions"), 0)
	check(t, "hummus label", hummus["label"], "2 tablespoon")
	check(t, "hummus unit", hummus["unit"], "tablespoon")
	check(t, "hummus gram_weight", hummus["gram_weight"], 33.9)

	synthetic := item(t, get(t, "/v1/foods/"+syntheticID+"/portions"), 0)
	check(t, "foundation label keeps the comment out", synthetic["label"], "1 cup diced")
	check(t, "foundation description", synthetic["description"], "1 cup is 1/2 of the container")
}

func TestPortionsSurvey(t *testing.T) {
	got := get(t, "/v1/foods/2705383/portions")
	check(t, "zero gram survey portion is dropped", len(items(t, got)), 2)

	cup := item(t, got, 0)
	check(t, "survey label", cup["label"], "1 cup")
	check(t, "survey gram_weight", cup["gram_weight"], 246.0)
	check(t, "survey modifier", cup["modifier"], "10205")
	check(t, "survey amount", cup["amount"], nil)
	check(t, "survey unit", cup["unit"], nil)
}

func TestPortionsBranded(t *testing.T) {
	got := get(t, "/v1/foods/"+oilID+"/portions")
	check(t, "branded food has one synthesized serving", len(items(t, got)), 1)

	milliliters := item(t, got, 0)
	check(t, "ml id", milliliters["id"], nil)
	check(t, "ml unit", milliliters["unit"], "ml")
	check(t, "ml gram_weight", milliliters["gram_weight"], nil)
	check(t, "ml amount", milliliters["amount"], 15.0)
	check(t, "ml label", milliliters["label"], "1 Tbsp (15 ml)")

	grams := item(t, get(t, "/v1/foods/"+soupID+"/portions"), 0)
	check(t, "g unit", grams["unit"], "g")
	check(t, "g gram_weight", grams["gram_weight"], 245.0)
	check(t, "g label", grams["label"], "1 cup (245 g)")

	// USDA also writes grams as GRM and GM, and millilitres as MLT. The API
	// promises g and ml, so those spellings are normalized.
	pizza := item(t, get(t, "/v1/foods/2456687/portions"), 0)
	check(t, "GRM unit", pizza["unit"], "g")
	check(t, "GRM gram_weight", pizza["gram_weight"], 156.0)
	check(t, "GRM label", pizza["label"], "1 Pizza (156g)")

	water := item(t, get(t, "/v1/foods/2457171/portions"), 0)
	check(t, "MLT unit", water["unit"], "ml")
	check(t, "MLT gram_weight", water["gram_weight"], nil)
	check(t, "label falls back to size and unit", water["label"], "355 ml")

	expectError(t, "unknown id", get(t, "/v1/foods/1/portions"), http.StatusNotFound, "not_found")
}

func TestScore(t *testing.T) {
	// 300 g spinach + 10 g oil, 4 servings.
	// Iron: 2.71*3 + 0*0.1 = 8.13 total, 2.0325 per serving.
	// Energy: 23*3 + 884*0.1 = 157.4 total.
	// Protein: 2.86*3 = 8.58, oil has no protein row and is listed missing.
	body := `{"servings":4,"items":[{"fdc_id":168462,"grams":300},{"fdc_id":1105904,"grams":10}]}`

	got := post(t, "/v1/score", body)
	check(t, "status", got.status, http.StatusOK)
	check(t, "servings", got.object["servings"], 4.0)
	check(t, "total_grams", got.object["total_grams"], 310.0)

	iron := asMap(t, asMap(t, got.object["total"])["1089"])
	check(t, "iron unit", iron["unit"], "MG")
	check(t, "iron name", iron["name"], "Iron, Fe")

	checkNear(t, "iron total", amount(t, got, "total", "1089"), 8.13)
	checkNear(t, "iron per serving", amount(t, got, "per_serving", "1089"), 2.0325)
	checkNear(t, "energy total", amount(t, got, "total", "1008"), 157.4)
	checkNear(t, "protein total", amount(t, got, "total", "1003"), 8.58)

	for _, nutrientID := range []string{"1008", "1003", "1005", "2000", "1079", "1087", "1089", "1093"} {
		amount(t, got, "total", nutrientID)
		amount(t, got, "per_serving", nutrientID)
	}

	missing := asList(t, got.object["missing"])
	check(t, "missing count", len(missing), 1)

	entry := asMap(t, missing[0])
	check(t, "missing fdc_id", entry["fdc_id"], 1105904.0)
	check(t, "missing nutrient_id", entry["nutrient_id"], 1003.0)
}

func TestScoreEnergyFallbackAndFractionalServings(t *testing.T) {
	// 200 g of the synthetic Foundation food: energy from 2048 = 150 per 100 g.
	got := post(t, "/v1/score", `{"servings":0.5,"items":[{"fdc_id":9000001,"grams":200}]}`)

	checkNear(t, "energy via fallback", amount(t, got, "total", "1008"), 300)
	checkNear(t, "half a serving doubles the per serving value", amount(t, got, "per_serving", "1008"), 600)
	check(t, "missing nutrient contributes zero", amount(t, got, "total", "1093"), 0.0)

	missing := asList(t, got.object["missing"])
	check(t, "missing count", len(missing), 1)
	check(t, "sodium is missing", asMap(t, missing[0])["nutrient_id"], 1093.0)
}

func TestScoreValidation(t *testing.T) {
	many := strings.Repeat(`{"fdc_id":168462,"grams":1},`, 501)

	cases := map[string]string{
		"servings zero":   `{"servings":0,"items":[{"fdc_id":168462,"grams":1}]}`,
		"servings absent": `{"items":[{"fdc_id":168462,"grams":1}]}`,
		"no items":        `{"servings":1,"items":[]}`,
		"too many items":  `{"servings":1,"items":[` + strings.TrimSuffix(many, ",") + `]}`,
		"negative grams":  `{"servings":1,"items":[{"fdc_id":168462,"grams":-1}]}`,
		"grams too large": `{"servings":1,"items":[{"fdc_id":168462,"grams":1000001}]}`,
		"servings tiny":   `{"servings":0.0001,"items":[{"fdc_id":168462,"grams":1}]}`,
		"servings huge":   `{"servings":1000001,"items":[{"fdc_id":168462,"grams":1}]}`,
		"trailing data":   `{"servings":1,"items":[{"fdc_id":168462,"grams":1}]} trailing`,
		"second object":   `{"servings":1,"items":[{"fdc_id":168462,"grams":1}]}{}`,
		"bad fdc_id":      `{"servings":1,"items":[{"fdc_id":0,"grams":1}]}`,
		"unknown fdc_id":  `{"servings":1,"items":[{"fdc_id":1,"grams":1},{"fdc_id":2,"grams":1}]}`,
		"unknown field":   `{"servings":1,"items":[{"fdc_id":168462,"grams":1}],"extra":true}`,
		"not json":        `servings=1`,
	}
	for name, body := range cases {
		got := post(t, "/v1/score", body)
		expectError(t, name, got, http.StatusBadRequest, "bad_request")

		if name == "unknown fdc_id" && !strings.Contains(errorMessage(t, got), "1, 2") {
			t.Errorf("unknown ids must be named: %v", got.object)
		}
	}

	got := post(t, "/v1/score", `{"servings":1,"items":[{"fdc_id":168462,"grams":0}]}`)
	check(t, "zero grams is allowed", got.status, http.StatusOK)
	check(t, "zero grams total", got.object["total_grams"], 0.0)

	got = post(t, "/v1/score", `{"servings":1000000,"items":[{"fdc_id":168462,"grams":1000000}]}`)
	check(t, "the bounds themselves are allowed", got.status, http.StatusOK)
}

func TestScoreRepeatedFoodReportsMissingOnce(t *testing.T) {
	oil := `{"fdc_id":` + oilID + `,"grams":10}`
	once := post(t, "/v1/score", `{"servings":1,"items":[`+oil+`]}`)
	twice := post(t, "/v1/score", `{"servings":1,"items":[`+oil+`,`+oil+`]}`)

	check(t, "once status", once.status, http.StatusOK)
	check(t, "twice status", twice.status, http.StatusOK)
	check(t, "grams add up", twice.object["total_grams"], 20.0)

	onceMissing := asList(t, once.object["missing"])
	twiceMissing := asList(t, twice.object["missing"])
	check(t, "missing is per food, not per item", len(twiceMissing), len(onceMissing))

	if len(onceMissing) == 0 {
		t.Fatal("the oil fixture is expected to lack a nutrient, or this test proves nothing")
	}
}

// TestScoreAgreesWithFoodOnANullDuplicate clears the lower row of the
// duplicate calcium pair. The detail then shows calcium as null, and the
// score must list it as missing instead of reading the higher row.
func TestScoreAgreesWithFoodOnANullDuplicate(t *testing.T) {
	server, _ := fixtureServerWith(t, api.Options{}, `UPDATE food_nutrient SET amount = NULL WHERE id = 1362463`)
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)

	detail := send(t, ts, http.MethodGet, "/v1/foods/168462", "")
	check(t, "detail status", detail.status, http.StatusOK)

	calcium := asMap(t, asMap(t, parse(t, detail)["nutrients"])["1087"])
	if calcium["amount"] != nil {
		t.Fatalf("detail calcium = %v, want null", calcium["amount"])
	}

	scored := send(t, ts, http.MethodPost, "/v1/score", `{"servings":1,"items":[{"fdc_id":168462,"grams":100}]}`)
	check(t, "score status", scored.status, http.StatusOK)

	object := parse(t, scored)
	check(t, "score calcium total", asMap(t, asMap(t, object["total"])["1087"])["amount"], 0.0)

	var listed bool

	for _, entry := range asList(t, object["missing"]) {
		pair := asMap(t, entry)
		if pair["fdc_id"] == 168462.0 && pair["nutrient_id"] == 1087.0 {
			listed = true
		}
	}

	if !listed {
		t.Fatalf("missing lacks calcium for 168462: %v", object["missing"])
	}
}

func TestScoreDecodeMessagesNameTheJSONProblem(t *testing.T) {
	const item = `{"fdc_id":168462,"grams":1}`

	cases := map[string]struct{ body, want string }{
		"not json":         {`servings=1`, "malformed JSON at offset"},
		"ends early":       {`{"servings":1,"items":[`, "ends early"},
		"wrong type":       {`{"servings":"four","items":[` + item + `]}`, `field "servings" has the wrong type`},
		"nested type":      {`{"servings":1,"items":[{"fdc_id":1,"grams":"x"}]}`, `field "items.grams" has the wrong type`},
		"array not object": {`[]`, "must be a JSON object"},
		"unknown field":    {`{"servings":1,"items":[` + item + `],"extra":true}`, `unknown field "extra"`},
		"empty":            {``, "empty"},
		"null":             {`null`, "servings must be between"},
	}

	for name, tc := range cases {
		got := post(t, "/v1/score", tc.body)
		expectError(t, name, got, http.StatusBadRequest, "bad_request")

		message := errorMessage(t, got)
		if !strings.Contains(message, tc.want) {
			t.Errorf("%s: want %q in %q", name, tc.want, message)
		}

		for _, leak := range []string{"Go ", "scoreRequest", "float64"} {
			if strings.Contains(message, leak) {
				t.Errorf("%s: %q leaks into the message: %q", name, leak, message)
			}
		}
	}
}

func TestHostOutsideLoopbackIsForbidden(t *testing.T) {
	for _, host := range []string{"127.0.0.1:8471", "localhost", "LOCALHOST:1", "[::1]:8471", "::1"} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/v1/about", nil)
		if err != nil {
			t.Fatal(err)
		}

		req.Host = host

		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}

		_ = resp.Body.Close()

		check(t, host+" is served", resp.StatusCode, http.StatusOK)
	}

	// An empty Host cannot be sent through the client, which fills it from
	// the URL, so it is covered by the hostOnly table instead.
	for _, host := range []string{"evil.example", "evil.example:8471", "127.0.0.1.nip.io", "127.0.0.1.evil.example:80"} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/v1/about", nil)
		if err != nil {
			t.Fatal(err)
		}

		req.Host = host

		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}

		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		check(t, host+" status", resp.StatusCode, http.StatusForbidden)
		check(t, host+" attribution still present", resp.Header.Get("X-Data-Attribution") != "", true)

		if !strings.Contains(string(raw), `"code":"forbidden"`) {
			t.Errorf("%s: body %s", host, raw)
		}
	}
}

func TestEmptyHostOptionIsIgnored(t *testing.T) {
	// -listen :8471 hands an empty host through. It must not whitelist a
	// request that carries no Host header.
	server, _ := fixtureServer(t, api.Options{Hosts: []string{""}})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/about", nil)
	req.Host = ""
	recorder := httptest.NewRecorder()

	server.Handler().ServeHTTP(recorder, req)
	check(t, "no Host header", recorder.Code, http.StatusForbidden)
}

func TestImportedReportsReleaseAndTime(t *testing.T) {
	server, database := fixtureServer(t, api.Options{})

	metadata, err := store.Meta(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}

	dataVersion, importedAt := server.Imported()
	check(t, "data version", dataVersion, meta.DataVersion)
	check(t, "imported at matches the metadata", importedAt, metadata[meta.KeyImportedAt])

	if _, err := time.Parse(time.RFC3339, importedAt); err != nil {
		t.Errorf("imported_at %q is not RFC 3339: %v", importedAt, err)
	}
}

func TestExtraHostsAreServed(t *testing.T) {
	server, _ := fixtureServer(t, api.Options{Hosts: []string{"192.168.1.5:8471"}})

	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/v1/about", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Host = "192.168.1.5:9999"

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	check(t, "listen host served on any port", resp.StatusCode, http.StatusOK)
}

func TestCancelledRequestIsNotAServerFault(t *testing.T) {
	_, _, logBuffer, handler := newLoggedServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/foods/168462", nil)
	req.Host = "127.0.0.1"
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	check(t, "status", recorder.Code, http.StatusInternalServerError)

	logged := logBuffer.String()
	if !strings.Contains(logged, "msg=\"request cancelled\"") || !strings.Contains(logged, "level=INFO") {
		t.Fatalf("a cancelled request is logged at info:\n%s", logged)
	}

	if strings.Contains(logged, "request failed") {
		t.Fatalf("a cancelled request is not a failure:\n%s", logged)
	}
}

func TestSearchFullBarcodeHitsOnce(t *testing.T) {
	got := get(t, "/v1/foods?q=00012345678905")
	check(t, "one hit for a barcode that is also indexed text", len(items(t, got)), 1)
	check(t, "fdc_id", item(t, got, 0)["fdc_id"], 1105906.0)
}

func TestScoreBodyTooLarge(t *testing.T) {
	padding := strings.Repeat(" ", 1<<20)
	got := post(t, "/v1/score", `{"servings":1,`+padding+`"items":[{"fdc_id":168462,"grams":1}]}`)
	expectError(t, "oversized body", got, http.StatusRequestEntityTooLarge, "bad_request")
}

func TestSearchQueryLengthCapped(t *testing.T) {
	tooLong := strings.Repeat("é", 201)
	expectError(t, "201 runes", get(t, "/v1/foods?q="+url.QueryEscape(tooLong)), http.StatusBadRequest, "bad_request")

	check(t, "200 runes pass validation", get(t, "/v1/foods?q="+url.QueryEscape(tooLong[:400])).status, http.StatusOK)
}

// rawReply is a response for tests that look at headers or exact body bytes.
type rawReply struct {
	status int
	header http.Header
	body   []byte
}

// send issues one request against any test server, reads and closes the
// body, and returns what the test needs from the response.
func send(t *testing.T, ts *httptest.Server, method, path, body string) rawReply {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	return rawReply{status: resp.StatusCode, header: resp.Header, body: raw}
}

// parse decodes a raw reply's body as the JSON object every endpoint sends.
func parse(t *testing.T, got rawReply) map[string]any {
	t.Helper()

	var object map[string]any
	if err := json.Unmarshal(got.body, &object); err != nil {
		t.Fatalf("body is not a JSON object: %v\n%s", err, got.body)
	}

	return object
}

func TestResponseHeaders(t *testing.T) {
	got := send(t, srv, http.MethodGet, "/v1/about", "")

	check(t, "nosniff", got.header.Get("X-Content-Type-Options"), "nosniff")
	check(t, "content type", got.header.Get("Content-Type"), "application/json; charset=utf-8")
}

func TestMethodNotAllowedCarriesAllow(t *testing.T) {
	cases := map[string]struct {
		method, path, allow string
	}{
		"about":    {http.MethodDelete, "/v1/about", "GET"},
		"score":    {http.MethodGet, "/v1/score", "POST"},
		"portions": {http.MethodPut, "/v1/foods/168462/portions", "GET"},
	}

	for name, tc := range cases {
		got := send(t, srv, tc.method, tc.path, "")

		check(t, name+" status", got.status, http.StatusMethodNotAllowed)
		check(t, name+" allow", got.header.Get("Allow"), tc.allow)
	}
}

// newLoggedServer builds a second server over its own database with a logger
// that writes to the returned buffer, so a test can break the database and
// read what was logged.
func newLoggedServer(t *testing.T) (*httptest.Server, *sql.DB, *bytes.Buffer, http.Handler) {
	t.Helper()

	var logBuffer bytes.Buffer

	server, database := fixtureServer(t, api.Options{
		Version: "logged",
		Logger:  logging.New(&logBuffer, logging.Options{}),
	})

	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)

	return ts, database, &logBuffer, server.Handler()
}

// alter runs statements against a finished database over a writable
// connection, closed again before the server opens the file immutable.
func alter(t *testing.T, path string, statements []string) {
	t.Helper()

	if len(statements) == 0 {
		return
	}

	database, err := sql.Open("sqlite", store.DSN(path, ""))
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = database.Close() }()

	for _, statement := range statements {
		if _, err := database.ExecContext(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
}

// fixtureServer imports the fixture into its own database and builds a
// server over it with opts, for tests that need what the shared server
// lacks: another logger, other hosts, or a database they can break.
func fixtureServer(t *testing.T, opts api.Options) (*api.Server, *sql.DB) {
	t.Helper()

	return fixtureServerWith(t, opts)
}

// fixtureServerWith is fixtureServer with SQL statements run against the
// finished database before it is opened read-only, to stand in for data
// shapes the fixture does not ship.
func fixtureServerWith(t *testing.T, opts api.Options, statements ...string) (*api.Server, *sql.DB) {
	t.Helper()

	out := filepath.Join(t.TempDir(), "fdc.sqlite")

	options := importer.Options{Src: "../importer/testdata/fixture", Out: out}
	if _, err := importer.Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}

	alter(t, out, statements)

	database, err := store.OpenReadOnly(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	server, err := api.New(context.Background(), database, opts)
	if err != nil {
		t.Fatal(err)
	}

	return server, database
}

func TestEveryRequestIsLogged(t *testing.T) {
	ts, _, logBuffer, _ := newLoggedServer(t)

	send(t, ts, http.MethodGet, "/v1/foods?q=spin", "")

	line := logBuffer.String()
	for _, want := range []string{"msg=request", "method=GET", "path=/v1/foods", "status=200", "duration="} {
		if !strings.Contains(line, want) {
			t.Errorf("request log lacks %s:\n%s", want, line)
		}
	}
}

func TestInternalErrorsAreLoggedNotLeaked(t *testing.T) {
	ts, database, logBuffer, _ := newLoggedServer(t)

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	// /v1/about is computed once in New, so it keeps answering.
	check(t, "about survives a broken database", send(t, ts, http.MethodGet, "/v1/about", "").status, http.StatusOK)

	for _, path := range []string{"/v1/foods?q=spin", "/v1/foods/168462", "/v1/foods/168462/portions"} {
		got := send(t, ts, http.MethodGet, path, "")

		check(t, path+" status", got.status, http.StatusInternalServerError)
		check(t, path+" body", string(got.body), `{"error":{"code":"internal","message":"internal error"}}`)
	}

	got := send(t, ts, http.MethodPost, "/v1/score", `{"servings":1,"items":[{"fdc_id":168462,"grams":1}]}`)

	check(t, "score status", got.status, http.StatusInternalServerError)
	check(t, "score body", string(got.body), `{"error":{"code":"internal","message":"internal error"}}`)

	logged := logBuffer.String()
	if !strings.Contains(logged, "msg=\"request failed\"") || !strings.Contains(logged, "closed") {
		t.Fatalf("the real error must be logged server side:\n%s", logged)
	}

	if strings.Count(logged, "status=500") != 4 {
		t.Fatalf("each failure logs one request line with status 500:\n%s", logged)
	}
}

func TestResponsesAreUnescapedUTF8(t *testing.T) {
	got := get(t, "/v1/foods/9000004")
	if !bytes.Contains(got.raw, []byte("Crème fraîche")) {
		t.Fatalf("description should be plain UTF-8: %s", got.raw)
	}
}

func TestNewRejectsDatabaseWithoutNutrients(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "empty.sqlite")

	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := database.ExecContext(ctx, store.Schema); err != nil {
		t.Fatal(err)
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	readOnly, err := store.OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = readOnly.Close() }()

	if _, err := api.New(ctx, readOnly, api.Options{}); !errors.Is(err, api.ErrNutrientMissing) {
		t.Fatal("a database without the scored nutrients must be refused")
	}
}
