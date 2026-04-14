# wx-1002 Task: Render Release Banner In The UI

Parent design: `wx-1000`

Depends on: `wx-1001`

## Goal

Show the release banner on the home page when the API payload is populated.

## Acceptance Criteria

- The banner renders the text and severity from the API response.
- Empty payloads render nothing.
- Existing home page layout still works on mobile width.

## Likely Files

- `web/src/components/release-banner.tsx`
- `web/src/pages/home.tsx`
- `web/src/components/release-banner.test.tsx`
