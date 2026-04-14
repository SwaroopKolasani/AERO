import json
from pathlib import Path

import jsonschema

# Schema files live alongside this module: one .schema.json file per artifact type.
_HERE = Path(__file__).parent

# Schemas are loaded once per process and cached; they do not change at runtime.
_cache: dict[str, dict] = {}


class UnknownArtifactType(KeyError):
    """Raised when no schema file exists for the requested artifact type."""


class SchemaValidationError(Exception):
    """Raised when obj does not conform to the named artifact schema."""

    def __init__(self, artifact_type: str, cause: jsonschema.ValidationError) -> None:
        super().__init__(f"{artifact_type}: {cause.message}")
        self.artifact_type = artifact_type
        self.validator_error = cause


def validate(artifact_type: str, obj: object) -> None:
    """Validate obj against the schema for artifact_type.

    Raises UnknownArtifactType  if no .schema.json file exists for artifact_type.
    Raises SchemaValidationError if obj does not conform to the schema.
    Raises json.JSONDecodeError  if the schema file contains invalid JSON.
    """
    schema_path = _HERE / f"{artifact_type}.schema.json"
    if not schema_path.is_file():
        raise UnknownArtifactType(artifact_type)
    if artifact_type not in _cache:
        _cache[artifact_type] = json.loads(schema_path.read_text(encoding="utf-8"))
    try:
        jsonschema.validate(obj, _cache[artifact_type])
    except jsonschema.ValidationError as exc:
        raise SchemaValidationError(artifact_type, exc) from exc