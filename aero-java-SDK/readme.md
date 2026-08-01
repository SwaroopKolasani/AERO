the main purpose of creating the java-client-sdk is to increase the exposure regarding the AERO for java developers.

here we will be implementing: 

## aero-models: defines stable java records and enums for aero request, response, reciepts, error and control plane contracts.

## aero-client: calls aero-cache without handling raw http, json, proof header
## aero-streaming: provides SSE token streaming, cancellation, concurrency, timeout handling and final reciept parsing.

## aero-spring-boot-starter : makes Aero available in Spring Boot through configuration and dependency injection instead of manual client setup.

## aero-resilience4j : adds retries, circuit breakers, bulkheads, and time limits while preserving Aero’s fail-open behavior and exact request identity.

## aero-observability : sends cache hits, misses, verification, latency, token, and cost metrics into Micrometer, Prometheus, Grafana, and OpenTelemetry.

## aero-testcontainers : proves Java integrations against real AeroCache, AeroCore, Valkey, and mock inference backends instead of relying only on mocks.

## aerocore-client : allows Java operators and backend services to use AeroCore placement, registration, health, and readiness contracts without recreating placement logic.
