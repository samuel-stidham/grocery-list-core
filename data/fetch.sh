#!/usr/bin/env bash
# Download the pinned FoodData Central release, verify it, and extract the
# files the importer reads. Safe to rerun: a verified archive is not downloaded
# again, and extraction overwrites in place.
set -euo pipefail

RELEASE="FoodData_Central_csv_2026-04-30"
URL="https://fdc.nal.usda.gov/fdc-datasets/${RELEASE}.zip"
# USDA publishes no checksum. This one was taken from the archive downloaded
# on 2026-10-26 and is the value the import is pinned to.
SHA256="c33d91e5c44ddbcd71ed6038714fb648b26089b3f91fabfa3be63d96d5763a01"

# The files the importer reads. Everything else in the archive stays zipped.
FILES=(
  food.csv nutrient.csv food_nutrient.csv food_portion.csv
  measure_unit.csv food_category.csv wweia_food_category.csv food_nutrient_derivation.csv
  sr_legacy_food.csv foundation_food.csv survey_fndds_food.csv branded_food.csv
  "Download API Field Descriptions.xlsx"
)

cd "$(dirname "$0")"
zip="${RELEASE}.zip"

checksum() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

if [ -f "$zip" ] && [ "$(checksum "$zip")" = "$SHA256" ]; then
  echo "have $zip, checksum ok"
else
  echo "downloading $URL"
  curl -fL --retry 3 -o "$zip.part" "$URL"
  mv "$zip.part" "$zip"
  got="$(checksum "$zip")"
  if [ "$got" != "$SHA256" ]; then
    echo "checksum mismatch for $zip" >&2
    echo "  want $SHA256" >&2
    echo "  got  $got" >&2
    exit 1
  fi
  echo "checksum ok"
fi

members=()
for f in "${FILES[@]}"; do
  members+=("${RELEASE}/${f}")
done
echo "extracting ${#FILES[@]} files into ${RELEASE}/"
unzip -o -q "$zip" "${members[@]}"
echo "done: $(du -sh "$RELEASE" | cut -f1) in $RELEASE/"
