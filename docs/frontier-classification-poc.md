# Frontier Classification POC

Date: 2026-05-28

## Purpose

Prove `photoscrawl` as a local-first Apple Photos crawler that can extract
human-useful image understanding for a user-owned life archive. The crawler
should answer:

- what is visible in this image;
- why the image may matter to the user;
- what evidence supports each observation;
- what remains uncertain;
- what higher-level clustering could use later.

`photoscrawl` stores source facts, observations, and evidence. It does not create
durable people, trip, place, relationship, or life-event truth in this POC.
Those belong in a later `lifecrawler` layer built from reviewable evidence.

Private sample images and model outputs stay outside the repo. Use real samples
only in temp storage and chat summaries.

## Required First Step

Use `@Browser` with **GPT-5.5 Pro only**. No substitutes. If GPT-5.5 Pro is not
available, stop and report that blocker before doing implementation.

Ask GPT-5.5 Pro to design the extraction ontology for real-world Apple Photos
images, using the two user-provided Singapore examples as calibration:

- a Lau Pa Sat / Satay Street meal photo;
- a Gardens by the Bay Supertree Grove night photo.

The design request should ask for concrete, schema-oriented output:

- source facts versus model observations;
- observation types and fields;
- evidence pointers;
- uncertainty and hallucination handling;
- privacy sensitivity flags;
- clustering features for a later Discrawl-style layer;
- what belongs in `photoscrawl`;
- what belongs later in `lifecrawler`.

The target bar is frontier multimodal understanding, not coarse Apple Vision
labels. Apple Vision can be a cheap local sensor, but it is not the product
quality bar.

## POC Question

Can `photoscrawl` produce observations good enough for useful personal search,
review, and later clustering?

Bad output:

- generic labels like `food`, `document`, `people`;
- ungrounded guesses without evidence;
- OCR dumps with no summary or usefulness judgment;
- model prose that cannot be queried;
- durable identity, trip, or place claims in the source crawler.

Good output:

- `scene_summary`: "Outdoor hawker-table meal with satay skewers, grilled
  prawns, dipping sauces, Lau Pa Sat plate, street-night setting."
- `place_candidate`: "Lau Pa Sat / Satay Street, Singapore" with evidence from
  visible text and visual context, marked candidate.
- `document_summary`: "Dutch immigration/residency document; contains issue
  date and work-permission language" with OCR evidence snippets.
- `event_context_candidate`: "Singapore travel meal" from time/location/photo
  clusters, marked candidate for later `lifecrawler`.
- `privacy_sensitivity`: identity document, address-bearing letter, face,
  child, license plate, payment receipt, health/money/sensitive document.
- `uncertainty`: where text is low-confidence, landmark inference is ambiguous,
  or image quality is poor.

## Classifier Tiers

Tier 0: source metadata.

- Always local.
- Photos asset metadata, timestamps, GPS, dimensions, albums, resource
  availability, burst/live-photo/source relationships, local derivative paths.
- No model inference.

Tier 1: cheap local sensors.

- Local OCR, barcode detection, face boxes, image quality, feature prints,
  coarse labels where useful.
- These are evidence helpers, not the final understanding layer.
- Face identity is out of scope; only anonymous boxes/counts.

Tier 2: local multimodal model.

- Find the installed Gemma/Gemma-like multimodal model if present.
- If present, run it on a bounded sample of user-approved local Photos images.
- Compare output to the frontier-model ontology.
- Keep prompts, reports, temp DBs, and private outputs outside the repo.

Tier 3: cloud frontier enrichment.

- Explicit opt-in only.
- Never default.
- Record exactly which asset derivative left the machine, resize dimensions,
  byte estimate, provider, model, prompt version, and consent flag.
- Use only when the user explicitly chooses a batch.

## Real Sample Plan

Use a bounded private sample outside the repo. The user approved using recent
travel photos from the local Photos library. Prefer a mixed sample large enough
to expose failure modes:

- landmarks / travel scenes;
- meals / restaurants / receipts;
- documents / identity-like documents;
- screenshots;
- people / group scenes;
- vehicles / street signs / maps;
- low-light or blurry images.

Start with 20-50 images. If that works, scale to 100-200 for distributional
signal. Do not write filenames, exact coordinates, OCR dumps, raw model outputs,
or generated private reports into git.

## Proposed Observation Ontology

Keep source facts separate from classifier output.

Source facts:

- asset id;
- local source identifier;
- media type;
- created/added/modified timestamps;
- timezone;
- dimensions;
- duration;
- favorite/hidden flags;
- album membership;
- raw GPS;
- resource availability;
- local derivative/original path class;
- burst/live-photo/source relationships.

Model observations:

- `scene_summary`;
- `object_activity_tags`;
- `visible_text_summary`;
- `ocr_snippet` with confidence and bounding box;
- `document_type_candidate`;
- `place_candidate`;
- `landmark_candidate`;
- `meal_food_candidate`;
- `merchant_or_venue_candidate`;
- `event_context_candidate`;
- `screenshot_app_candidate`;
- `face_count`;
- `anonymous_face_box`;
- `barcode_payload`;
- `privacy_sensitivity`;
- `quality_issue`;
- `model_uncertainty`;
- `clustering_feature`.

Each observation must include:

- observation id;
- asset id;
- observation type;
- value or label;
- confidence;
- source tier;
- model id;
- prompt version or sensor version;
- evidence id;
- optional bounding box;
- uncertainty note where applicable.

## Implementation POC

After the GPT-5.5 Pro design pass, implement the smallest useful slice.

Preferred path:

1. Add a bounded experimental classification path under the existing
   `classify` flow.
2. Resolve local image derivatives/originals without iCloud downloads.
3. Run the local multimodal model if available.
4. Store typed observations and evidence in SQLite, or write a temp-only report
   if the storage ontology must be revised first.
5. Feed outputs to another model/sub-agent for critique against the product
   goal: useful for search, clustering, and `lifecrawler`, or junk.
6. Deslop existing metadata/observation code when it conflicts with the design.

Do not build a broad new architecture before the sample proves value. Avoid
new CLIs, scripts, knobs, compatibility layers, and speculative abstractions.

## Success Criteria

The POC succeeds only if it produces real-world outputs that a human can judge:

- at least 20 real private sample images processed outside the repo;
- at least 5 good examples summarized in chat;
- at least 5 bad or hallucinated examples summarized in chat;
- clear comparison between local model output and frontier expectations;
- concrete recommendation on whether local multimodal classification is useful;
- schema/design changes grounded in sample evidence;
- no private data committed;
- no cloud calls unless explicitly approved for the sample;
- `devenv shell verify` passes;
- privacy scan passes;
- banned-framing scan passes;
- one surgical commit and push when clean.

## Public Repo Boundary

Tracked files may contain:

- public-safe ontology;
- synthetic examples;
- model/provider abstraction shape;
- prompts without private data;
- tests with synthetic fixtures.

Tracked files must not contain:

- private Photos images;
- private filenames;
- exact GPS from the user library;
- OCR dumps from private images;
- model outputs from private images;
- generated private reports;
- temp DBs or snapshots;
- language that frames the tool as coercive profiling or user-hostile analysis.
