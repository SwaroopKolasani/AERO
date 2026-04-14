"""
Tests for rig/schemas — T0.3a (mechanism) + T0.3b (production schemas).

Section 1: mechanism tests use controlled tmp_path schemas so they are
            independent of any production schema file.
Section 2: production schema tests load the committed example files from
            rig/schemas/examples/ and assert pass/fail against them.
"""

import json
from pathlib import Path
from unittest.mock import patch

import jsonschema
import pytest

from rig.schemas import validate, UnknownArtifactType, SchemaValidationError

_EXAMPLES = Path(__file__).parent.parent / "rig" / "schemas" / "examples"

_MINI_SCHEMA = {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "type": "object",
    "additionalProperties": False,
    "required": ["schema_version"],
    "properties": {
        "schema_version": {"type": "integer", "const": 1},
    },
}


@pytest.fixture(autouse=True)
def clear_cache():
    import rig.schemas as mod
    mod._cache.clear()
    yield
    mod._cache.clear()


def write_schema(tmp_path, name, schema):
    (tmp_path / f"{name}.schema.json").write_text(
        json.dumps(schema), encoding="utf-8"
    )


def load_example(filename: str) -> object:
    return json.loads((_EXAMPLES / filename).read_text(encoding="utf-8"))


# ═══════════════════════════════════════════════════════════════════════════
# 1. Mechanism tests
# ═══════════════════════════════════════════════════════════════════════════

def test_unknown_type_raises_named_error(tmp_path):
    with patch("rig.schemas._HERE", tmp_path):
        with pytest.raises(UnknownArtifactType):
            validate("does_not_exist", {})


def test_unknown_type_error_names_the_type(tmp_path):
    with patch("rig.schemas._HERE", tmp_path):
        with pytest.raises(UnknownArtifactType, match="missing_type"):
            validate("missing_type", {})


def test_unknown_type_is_subclass_of_key_error(tmp_path):
    with patch("rig.schemas._HERE", tmp_path):
        with pytest.raises(KeyError):
            validate("does_not_exist", {})


def test_valid_object_does_not_raise(tmp_path):
    write_schema(tmp_path, "widget", _MINI_SCHEMA)
    with patch("rig.schemas._HERE", tmp_path):
        validate("widget", {"schema_version": 1})


def test_valid_object_returns_none(tmp_path):
    write_schema(tmp_path, "widget", _MINI_SCHEMA)
    with patch("rig.schemas._HERE", tmp_path):
        assert validate("widget", {"schema_version": 1}) is None


def test_invalid_object_raises_schema_validation_error(tmp_path):
    write_schema(tmp_path, "widget", _MINI_SCHEMA)
    with patch("rig.schemas._HERE", tmp_path):
        with pytest.raises(SchemaValidationError):
            validate("widget", {"schema_version": "not-an-int"})


def test_schema_validation_error_carries_artifact_type(tmp_path):
    write_schema(tmp_path, "widget", _MINI_SCHEMA)
    with patch("rig.schemas._HERE", tmp_path):
        with pytest.raises(SchemaValidationError) as info:
            validate("widget", {})
    assert info.value.artifact_type == "widget"


def test_schema_validation_error_carries_underlying_jsonschema_error(tmp_path):
    write_schema(tmp_path, "widget", _MINI_SCHEMA)
    with patch("rig.schemas._HERE", tmp_path):
        with pytest.raises(SchemaValidationError) as info:
            validate("widget", {})
    assert isinstance(info.value.validator_error, jsonschema.ValidationError)


def test_schema_validation_error_is_distinct_from_jsonschema_error():
    assert not issubclass(SchemaValidationError, jsonschema.ValidationError)


def test_extra_field_raises_when_additional_properties_false(tmp_path):
    write_schema(tmp_path, "widget", _MINI_SCHEMA)
    with patch("rig.schemas._HERE", tmp_path):
        with pytest.raises(SchemaValidationError):
            validate("widget", {"schema_version": 1, "extra": "not allowed"})


def test_invalid_json_in_schema_file_raises(tmp_path):
    (tmp_path / "broken.schema.json").write_text("{not json", encoding="utf-8")
    with patch("rig.schemas._HERE", tmp_path):
        with pytest.raises(json.JSONDecodeError):
            validate("broken", {})


def test_schema_file_read_once(tmp_path):
    write_schema(tmp_path, "widget", _MINI_SCHEMA)
    with patch("rig.schemas._HERE", tmp_path):
        validate("widget", {"schema_version": 1})
        (tmp_path / "widget.schema.json").write_text("THIS IS NOT JSON", encoding="utf-8")
        validate("widget", {"schema_version": 1})


# ═══════════════════════════════════════════════════════════════════════════
# 2. Production schema tests — example files
# ═══════════════════════════════════════════════════════════════════════════

# ── decision_report ──────────────────────────────────────────────────────────

def test_decision_report_valid_example():
    validate("decision_report", load_example("decision_report.valid.json"))


def test_decision_report_invalid_extra_field():
    with pytest.raises(SchemaValidationError) as info:
        validate("decision_report", load_example("decision_report.invalid_extra_field.json"))
    assert info.value.artifact_type == "decision_report"
    assert "notes" in str(info.value.validator_error.path) or \
           "notes" in info.value.validator_error.message


def test_decision_report_invalid_verdict():
    with pytest.raises(SchemaValidationError) as info:
        validate("decision_report", load_example("decision_report.invalid_verdict.json"))
    assert info.value.artifact_type == "decision_report"
    assert "MAYBE" in info.value.validator_error.message


# ── fixture_manifest ─────────────────────────────────────────────────────────

def test_fixture_manifest_valid_example():
    validate("fixture_manifest", load_example("fixture_manifest.valid.json"))


def test_fixture_manifest_missing_seed():
    with pytest.raises(SchemaValidationError) as info:
        validate("fixture_manifest", load_example("fixture_manifest.missing_seed.json"))
    assert info.value.artifact_type == "fixture_manifest"
    assert "seed" in info.value.validator_error.message


# ── run_record ───────────────────────────────────────────────────────────────

def test_run_record_valid_example():
    validate("run_record", load_example("run_record.valid.json"))


def test_run_record_valid_example_opaque_run_identity_fields_accepted():
    # The valid example has extra fields in run_identity (run_ordinal, profile,
    # uuid). These must be accepted because run_identity is opaque beyond
    # schema_version.
    obj = load_example("run_record.valid.json")
    assert "run_ordinal" in obj["run_identity"]
    validate("run_record", obj)


def test_run_record_invalid_outcome_code():
    with pytest.raises(SchemaValidationError) as info:
        validate("run_record", load_example("run_record.invalid_outcome_code.json"))
    assert info.value.artifact_type == "run_record"
    assert "fail_network" in info.value.validator_error.message


def test_run_record_missing_schema_version_in_run_identity():
    with pytest.raises(SchemaValidationError) as info:
        validate(
            "run_record",
            load_example("run_record.unknown_run_identity_has_no_schema_version.json"),
        )
    assert info.value.artifact_type == "run_record"
    assert "schema_version" in info.value.validator_error.message


# ── postmortem ───────────────────────────────────────────────────────────────

def test_postmortem_valid_example():
    validate("postmortem", load_example("postmortem.valid.json"))


def test_postmortem_valid_example_nullable_fields():
    obj = load_example("postmortem.valid.json")
    assert obj["transfer_mode"] is None
    assert obj["invalidation_ref_if_any"] is None


def test_postmortem_freeform_text_field_rejected():
    # Regression guard: additionalProperties:false is the bounded-fields enforcer.
    with pytest.raises(SchemaValidationError) as info:
        validate("postmortem", load_example("postmortem.freeform_text_field.json"))
    assert info.value.artifact_type == "postmortem"
    assert "notes" in str(info.value.validator_error.path) or \
           "notes" in info.value.validator_error.message