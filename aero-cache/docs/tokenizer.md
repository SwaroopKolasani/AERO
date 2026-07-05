# Tokenizer and Chat Template

AeroCache Tier-A exact caching keys on token IDs, not raw prompt text.

For chat requests, the request path is:

```text
messages -> chat template renderer -> rendered prompt -> tokenizer -> token IDs -> cache key