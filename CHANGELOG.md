# Changelog

## 0.2.0

- Fixed Anthropic Messages tool loops dropping the top-level Gemini `thought_signature`, which caused `provider invoke failed` (503) on continuation turns; the signature now round-trips through both non-streaming and streaming adapters.
- Added OpenAI-compatible image generation.
- Added a Simplified Chinese README.
- Updated the module path to `github.com/jd-opensource/joytoken-sdk-go`.
- Added the `agent` subpackage with bounded tool loops and OpenAI/Anthropic JoyToken providers.
- Aligned the default 60-second request timeout with the TypeScript SDK.
- Added parsed response bodies and response headers to `APIError`.
- Added a clear local error when an authenticated endpoint is called without an API key.
- Added Anthropic Messages support.
- Added model metadata, model detail, and pricing helpers.
- Added streaming examples and production endpoint defaults.

## 0.1.0

- Initial JoyToken Go client and streaming support.
