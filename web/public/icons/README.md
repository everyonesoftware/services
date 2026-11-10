# Application icons

These PNG files are local copies of upstream artwork, not generated substitutes.
`sources.json` records each downloaded source URL, attribution owner, GitHub
account/repository identity checks, response content type, decoded dimensions,
retrieval time, original download SHA256, and final PNG SHA256.

The fifteen organization avatars were fetched from the avatar URL returned by
GitHub's public account API, after checking the corresponding application's
repository owner, description, and project website. Organization avatars can
represent a vendor rather than an individual product. The requested slugs are
preserved in the local filenames.

`uptimekuma.png` is the application's `public/icon-512x512.png`, explicitly listed
in its upstream web app manifest. Both its image and manifest URLs are pinned to
the same source commit recorded in `sources.json`. It is not Louis Lam's personal
GitHub avatar.

All images were decoded with Pillow, converted to RGBA, and saved as PNG without
resizing. Native upstream dimensions are retained; requesting a 512-pixel avatar
does not guarantee that GitHub supplies a 512-pixel image. Each final file was
reopened and verified, and its SHA256 was checked against the manifest.

## Artwork and trademarks

Names, logos, avatars, and artwork remain the property of their respective owners.
Their inclusion identifies applications and does not imply endorsement or grant
trademark rights. This directory does not assert a blanket artwork license.
Consult each upstream project for applicable licenses and brand guidelines before
redistributing or changing these assets. Attribution and upstream context URLs
are recorded separately for every image in `sources.json`.
