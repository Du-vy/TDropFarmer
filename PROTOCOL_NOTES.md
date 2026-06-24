# Protocol Notes

## Twitch Channel Points Bonus Claim

Date: 2026-06-24

Known Twitch web GraphQL persisted operations used by TDropFarmer:

- `ChannelPointsContext`
- Persisted query version: `1`
- SHA256 hash: `1530a003a7d374b0380b79db0be0534f30ff46e61cffa2bc0e2468a909fbc024`
- Variables: `channelLogin`

- `ClaimCommunityPoints`
- Persisted query version: `1`
- SHA256 hash: `46aaeebe02c99afdf4fc97c7c0cba964124bf6b0af229395f1f6d1feed05b3d0`
- Variables: `input.channelID`, `input.claimID`

Runtime flow:

- Load channel points context for each configured streamer.
- If `communityPoints.availableClaim.id` is present, emit a `bonus_available` internal event.
- Claim by submitting `ClaimCommunityPoints` with the channel ID and claim ID.
- Treat successful GraphQL completion as a claimed bonus and wait for later points events/balance refreshes for authoritative totals.

## Twitch Predictions (MakePrediction)

Date: 2026-06-24

- `MakePrediction`
- Persisted query version: `1`
- SHA256 hash: `b44682ecc88358817009f20e69d75081b1e58825bb40aa53d5dbadcc17c881d8`
- Variables: `input.eventID`, `input.outcomeID`, `input.points`, `input.transactionID`

## Playback Access Token (minute-watched)

Date: 2026-06-24

- `PlaybackAccessToken`
- Persisted query version: `1`
- SHA256 hash: `3093517e37e4f4cb48906155bcd894150aef92617939236d2508f3375ab732ce`
- Variables: `login`, `isLive`, `isVod`, `vodID`, `playerType`

Runtime flow:
- Obtain `signature` and `value` from the response.
- Build `https://usher.ttvnw.net/api/channel/hls/{login}.m3u8?sig={sig}&token={value}`
- Pick lowest quality HLS playlist URL.
- HEAD the last media segment URL to simulate streaming presence.
- Optionally POST to `spade_url` with base64-encoded `minute-watched` payload.
