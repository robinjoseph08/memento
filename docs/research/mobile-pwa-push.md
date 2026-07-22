# Mobile PWA push-notification constraints

Research current as of 2026-07-16. This note answers whether the proposed family photo portal can send push notifications when installed as a PWA, and what that should mean for product scope.

## Answer and recommendation

Yes. An installed PWA can receive standards-based Web Push notifications on both iPhone/iPad and Android.

The important asymmetry is installation:

- On iPhone and iPad, Web Push is available to a web app only after the recipient adds it to the Home Screen, and support begins with iOS/iPadOS 16.4. The installed app must then request permission in response to a direct interaction such as tapping an “Enable notifications” button. Notifications appear with native-app notifications on the Lock Screen and in Notification Center and participate in Focus settings. [WebKit: Web Push for Web Apps on iOS and iPadOS](https://webkit.org/blog/13878/web-push-for-web-apps-on-ios-and-ipados/)
- On Android, Chrome and Firefox support Web Push, including while the site is not open. Installation is not a Web Push prerequisite on Android: permission belongs to the website/origin, so a site opened in a browser tab can subscribe too. Installing the PWA still gives the family a more app-like launch point. This “installation is optional” conclusion is an inference from Chrome's site-notification model and the standards API, which contain no install gate; Chrome documents notification permission for websites, while Mozilla documents Web Push delivery in Firefox for Android. [Google Chrome Help: notifications on Android](https://support.google.com/chrome/answer/3220216?co=GENIE.Platform%3DAndroid&hl=en-XX), [Mozilla: Web Push notifications in Firefox](https://support.mozilla.org/en-US/kb/push-notifications-firefox), [Push API](https://w3c.github.io/push-api/)

Push should therefore supplement rather than replace email. Keep the portal's authenticated activity feed as the source of truth and email as the universal notification channel. The subsequent product decision includes push in MVP as an optional per-device channel, without making installation, browser support, a durable subscription, or best-effort background delivery prerequisites for learning that photos were published.

## Platform matrix

| Platform | Can receive Web Push? | Must install first? | Permission and delivery notes |
| --- | --- | --- | --- |
| iPhone/iPad 16.4+ | Yes | Yes: add the app to the Home Screen, then launch that installed web app | The permission request must follow direct recipient interaction. The recipient can later change the app's notification settings in iOS/iPadOS Settings. [WebKit](https://webkit.org/blog/13878/web-push-for-web-apps-on-ios-and-ipados/) |
| Android Chrome | Yes | No; installation is still desirable for the portal UX | Chrome grants notification permission per site, may use quiet permission UI, can remove permission for a site that has not been visited in a while, and sends no notifications in Incognito. [Google Chrome Help](https://support.google.com/chrome/answer/3220216?co=GENIE.Platform%3DAndroid&hl=en-XX) |
| Android Firefox | Yes | No; Firefox also supports installing web apps | Firefox for Android routes Web Push through Mozilla's service plus Google's mobile delivery infrastructure. That browser implementation detail is transparent to the portal's standards-based sender. [Mozilla Web Push help](https://support.mozilla.org/en-US/kb/push-notifications-firefox), [Mozilla Firefox Android web-app installation](https://support.mozilla.org/en-US/kb/use-web-apps-firefox-android) |
| Older iPhone/iPad, unsupported browser, or denied permission | No | N/A | Feature-detect `serviceWorker`, `PushManager`, and `Notification`; retain email and the in-portal feed. The Push API is HTTPS-only and individual API details still vary by browser. [MDN: PushManager](https://developer.mozilla.org/en-US/docs/Web/API/PushManager) |

On iOS/iPadOS, a web app manifest with `display: "standalone"` or `"fullscreen"` makes the saved site open as a Home Screen web app. A third-party browser can offer Add to Home Screen too; the resulting Home Screen app receives the same capability, so the onboarding text need not require Gmail or Google sign-in and need not strictly require that the invitation link first be opened in Safari. [WebKit](https://webkit.org/blog/13878/web-push-for-web-apps-on-ios-and-ipados/)

Browser and OS behavior can change independently of the portal, so the product should feature-detect capabilities and show device-specific instructions instead of inferring support solely from a user-agent string. WebKit explicitly recommends feature detection for the standards implementation. [WebKit](https://webkit.org/blog/13878/web-push-for-web-apps-on-ios-and-ipados/)

## Required standards flow

The portable implementation is the conventional Push API + Notifications API + service-worker flow:

1. Serve the portal over HTTPS and register a service worker. Push and service-worker capabilities are restricted to secure contexts. [MDN: PushManager](https://developer.mozilla.org/en-US/docs/Web/API/PushManager), [Service Workers specification](https://www.w3.org/TR/service-workers/#security-considerations)
2. After the recipient is signed in and understands the benefit, present an in-product “Enable notifications on this device” action. Call the permission/subscription API only from that interaction. Mozilla documents the user-gesture requirement and WebKit requires it for iPhone/iPad. [MDN: Using the Notifications API](https://developer.mozilla.org/en-US/docs/Web/API/Notifications_API/Using_the_Notifications_API), [WebKit](https://webkit.org/blog/13878/web-push-for-web-apps-on-ios-and-ipados/)
3. Call `registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey })`. The browser returns a `PushSubscription` containing an endpoint plus encryption material; send that complete subscription to the portal backend and associate it with the signed-in recipient and this device/browser installation. [Push API](https://w3c.github.io/push-api/), [web.dev: Subscribe a user](https://web.dev/articles/push-notifications-subscribing-a-user)
4. When publication occurs, the portal backend encrypts a small payload and POSTs it to each subscription endpoint using the Web Push protocol and VAPID authentication. The browser vendor's push service wakes the user agent and dispatches a `push` event to the service worker. [RFC 8030](https://www.rfc-editor.org/rfc/rfc8030.html), [RFC 8291](https://www.rfc-editor.org/rfc/rfc8291.html), [Push API](https://w3c.github.io/push-api/)
5. The handler calls `event.waitUntil(registration.showNotification(...))`; a `notificationclick` handler opens an authenticated event URL. Mobile browsers expect notifications to be shown through the service-worker registration. [MDN: Using the Notifications API](https://developer.mozilla.org/en-US/docs/Web/API/Notifications_API/Using_the_Notifications_API), [MDN: showNotification](https://developer.mozilla.org/en-US/docs/Web/API/ServiceWorkerRegistration/showNotification)

Each enabled device/browser has its own subscription. The server should model subscriptions as child records of a recipient, not put one endpoint directly on the recipient record. Logging out on a shared device should unsubscribe locally and remove that device's server record; “sign out all devices” should invalidate every subscription mapping as well as every portal session. This is a product consequence of the Push API's subscription being attached to a browser/service-worker registration rather than to the portal's account session. [Push API](https://w3c.github.io/push-api/)

## APNs, FCM, VAPID, and keys

The portal should implement one standards-based Web Push sender, not separate native Apple and Android notification integrations:

- Apple uses APNs underneath iOS/iPadOS Web Push, but the portal does not need an Apple Developer Program membership, an APNs certificate, or an APNs device token. It sends to the endpoint returned in `PushSubscription`. A restrictive outbound firewall must permit `*.push.apple.com`. [WebKit](https://webkit.org/blog/13878/web-push-for-web-apps-on-ios-and-ipados/)
- Chrome may use FCM as its browser push service, and Firefox Android combines Mozilla's service with Google's mobile delivery infrastructure, but a web application does not need its own Firebase project. The same standards request is sent to whichever endpoint the browser returns. [web.dev: Push FAQ](https://web.dev/articles/push-notifications-faq), [Mozilla Web Push help](https://support.mozilla.org/en-US/kb/push-notifications-firefox)
- Generate one long-lived VAPID P-256 key pair for the portal, expose the public key to subscription code, and protect the private key as a server secret. VAPID identifies and authenticates the application server to push services. [RFC 8292](https://www.rfc-editor.org/rfc/rfc8292.html), [web.dev: Subscribe a user](https://web.dev/articles/push-notifications-subscribing-a-user)
- Treat VAPID-key rotation as a migration: a subscription restricted to the old public key must be recreated for the replacement key. RFC 8292 explicitly requires a new subscription when replacing the signing key. [RFC 8292, section 4.2](https://www.rfc-editor.org/rfc/rfc8292.html#section-4.2)
- Web Push payloads are end-to-end encrypted against the subscription's `p256dh` key and `auth` secret, although the push service still observes metadata such as timing, size, and frequency. [RFC 8291](https://www.rfc-editor.org/rfc/rfc8291.html), [Push API security considerations](https://w3c.github.io/push-api/#security-and-privacy-considerations)

Use a maintained Web Push library rather than implementing RFC 8030/8291/8292 cryptography and headers directly.

## Subscription expiry and reconciliation

A push subscription is not permanent. The protocol says subscriptions have a limited lifetime and may be terminated by the push service or user agent at any time; `expirationTime` is frequently `null`, which means no known expiry timestamp rather than a guarantee that the subscription will never expire. [RFC 8030](https://www.rfc-editor.org/rfc/rfc8030.html), [Push API: `expirationTime`](https://w3c.github.io/push-api/#dom-pushsubscription-expirationtime), [web.dev: subscription expiration](https://web.dev/articles/push-notifications-subscribing-a-user#resubscribe_regularly_to_prevent_expiration)

The implementation should:

- Save the endpoint, keys, recipient, device/browser label, creation time, last successful send, and last reconciliation time.
- On every authenticated app launch, call `getSubscription()`, compare it with the server record, and upsert or remove the server record as needed.
- Listen for `pushsubscriptionchange` and attempt to synchronize replacements, but do not depend on that event alone: MDN marks it as not Baseline, and network synchronization from its handler can fail while offline. [MDN: `pushsubscriptionchange`](https://developer.mozilla.org/en-US/docs/Web/API/ServiceWorkerGlobalScope/pushsubscriptionchange_event)
- Remove endpoints when the push service returns 404 or 410. RFC 8030 permits services to expire subscriptions at any time and specifies 404 for an expired subscription; browser guidance also treats both 404 and 410 as terminal. [RFC 8030, subscription expiration](https://www.rfc-editor.org/rfc/rfc8030.html#section-7.3), [web.dev: Web Push HTTP status codes](https://web.dev/articles/push-notifications-common-issues-and-reporting-bugs#http_status_codes)
- Never send artificial notifications merely to keep an idle subscription alive. Chrome's guidance warns against defeating browser cleanup of forgotten subscriptions. [web.dev: subscription expiration](https://web.dev/articles/push-notifications-subscribing-a-user#resubscribe_regularly_to_prevent_expiration)

On Android Chrome specifically, recipients can unsubscribe from the notification itself, and Chrome may remove notification permission from sites they have not visited in a while. Reconciliation and email fallback are therefore necessary even when initial enrollment succeeded. [Google Chrome Help](https://support.google.com/chrome/answer/3220216?co=GENIE.Platform%3DAndroid&hl=en-XX)

## Background delivery and reliability limits

Web Push can wake a service worker while the page is closed, including on Android when the browser UI is closed. Push services can retain messages while a user agent is temporarily offline, but only for the requested TTL, and a push service may shorten that retention period. [Push API](https://w3c.github.io/push-api/), [web.dev: Push FAQ](https://web.dev/articles/push-notifications-faq), [RFC 8030, TTL](https://www.rfc-editor.org/rfc/rfc8030.html#section-5.2)

This does not create a continuously running background app. Service workers are event-driven and time-limited; the browser may terminate them when no event is pending or when work exceeds an implementation limit. Keep the push handler small, put asynchronous work inside `event.waitUntil(...)`, and treat the portal/server as the source of truth when the recipient opens the notification. [Service Workers specification: lifetime](https://www.w3.org/TR/service-workers/#service-worker-lifetime)

Use `userVisibleOnly: true` and display a notification for every conventional push. Chrome requires user-visible Web Push, and WebKit can revoke the subscription if a push handler fails to show a visible notification. This rules out using push as a general silent background-sync mechanism. [web.dev: `userVisibleOnly`](https://web.dev/articles/push-notifications-subscribing-a-user#uservisibleonly_options), [WebKit: Declarative Web Push](https://webkit.org/blog/16535/meet-declarative-web-push/)

WebKit's Declarative Web Push, available beginning with iOS/iPadOS 18.4, can provide a visible fallback notification even if service-worker JavaScript is unavailable. It is backward-compatible with a conventional JSON/service-worker handler, but it is not necessary for an MVP and should not replace the cross-browser implementation. [WebKit: Declarative Web Push](https://webkit.org/blog/16535/meet-declarative-web-push/)

Delivery is consequently best-effort, not a durable job queue. A device can be offline beyond TTL, the recipient or browser can revoke permission, a subscription can expire, Focus or OS settings can suppress presentation, and service-worker logic can fail. The notification should point to durable “newly published” state in the portal rather than be the only record that an update exists. [RFC 8030](https://www.rfc-editor.org/rfc/rfc8030.html), [WebKit: Focus support](https://webkit.org/blog/13878/web-push-for-web-apps-on-ios-and-ipados/)

## Permission and privacy UX

Do not show the browser permission prompt on first page load. First explain that notifications announce newly published family photos, then let the recipient press a clear device-specific button. Chrome recommends contextual, user-initiated prompts and can place origins with poor acceptance rates into quiet UI. [Chrome: notification permission UX](https://developer.chrome.com/blog/notification-permission-data-in-crux), [Chrome Lighthouse guidance](https://developer.chrome.com/docs/lighthouse/best-practices/notification-on-start)

Recommended onboarding states:

1. **iPhone/iPad, not installed:** explain Add to Home Screen; do not offer the browser permission action yet.
2. **Supported and installed/eligible:** offer “Enable notifications on this device.”
3. **Permission denied:** explain how to re-enable it in OS/browser settings; code cannot simply keep prompting after a denial. [web.dev: permission states](https://web.dev/articles/push-notifications-subscribing-a-user), [WebKit](https://webkit.org/blog/13878/web-push-for-web-apps-on-ios-and-ipados/)
4. **Unsupported, expired, or revoked:** keep email selected and show a non-alarming status in notification preferences.
5. **Public/shared device:** recommend email only; if the person still enables push, make logout remove that subscription.

Because Apple notifications can appear on the Lock Screen and Web Push services observe delivery metadata, use privacy-minimizing copy such as “New family photos are ready” rather than an event name, person name, face, or thumbnail. The notification's deep link must still pass through normal portal authentication and authorization; possession of a notification must never grant access. [WebKit: notification presentation](https://webkit.org/blog/13878/web-push-for-web-apps-on-ios-and-ipados/), [Push API security considerations](https://w3c.github.io/push-api/#security-and-privacy-considerations)

## Product implication for this effort

- Build the site responsive and installable as a PWA from the start: manifest, stable manifest `id`, icons, standalone display, HTTPS, and a service worker.
- Keep the email choices of immediate, weekly digest, or none as MVP notification behavior.
- Include push in MVP as a per-device opt-in. A Recipient can choose email, push, both, or neither without changing access.
- Preserve one durable in-portal “new since your last visit” view regardless of notification settings or delivery success.
- Coalesce immediate email and push over the same 15-minute window and include the same currently authorized Publication and Comment activity, never one notice per photo.

This keeps the low-friction email path for every relative while allowing frequent mobile users to install the portal and receive native-feeling alerts.
