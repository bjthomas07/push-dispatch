# Why we built this

Parse.com made push infrastructure approachable. Its shutdown announcement sent
developers looking for another home. On January 29, 2016, George Deglin announced
OneSignal's Parse migration tool. His Product Hunt launch described the replacement
as a free push service. Those messages helped make the move attractive.
[OneSignal's announcement](https://onesignal.com/blog/important-note-for-android-parse-push-users/),
[the founder's launch](https://www.producthunt.com/products/onesignal/forums).

Free tooling supported by data-driven improvements looked like a reasonable exchange
to us at the time. The documented history is more specific: OneSignal's 2018 policy
update discussed data models and monetization, and its 2019 announcement said it
would stop sharing data with advertisers. These sources do not establish that
customer data trained a particular recommendation model.
[2018 policy](https://onesignal.com/blog/product-policy-updates-for-gdpr/),
[2019 announcement](https://onesignal.com/blog/onesignal-will-no-longer-share-data-with-advertisers/).

Now its published plan update says mobile push and in-app messaging on the Free
plan are capped at **1,000 MAU per organization**, effective September 1, 2026 for new
customers and October 1, 2026 for existing customers. Web push and email are
unaffected by that particular change. This is the announced policy checked on
September 7, 2026; consult the provider for subsequent changes.
[OneSignal plan guide](https://onesignal.com/blog/which-onesignal-plan-is-best-for-you/amp/).

For our use case, that tradeoff no longer works. We are frustrated that another
pricing change means another migration and another round of real-device testing
for something that already worked. We want to spend that effort once, share the
result, and retain control over this part of our apps. The criticism is about the
cost and dependency of the model; this project makes no claim about anyone's motives.

The license has no MAU meter. Operating the service still costs resources, and
FCM remains an external delivery dependency. Keeping the storage schema and adapter
interfaces open makes the next infrastructure decision ours to make.
