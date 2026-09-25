Edits or revises an image using a text prompt plus one or more source images.

Provide the source image(s) via `image_ids` (ids returned by a previous ImageGenerate or ImageEdit call, of the form `img_1a2b3c4d5e6f`), `file_paths` (paths to image files on disk), or a mix of both. Between 1 and 16 source images total are required. Only PNG, JPEG, and WEBP images are accepted; each source file must be no larger than 20MB.

Returns the edited image inline in the conversation as a preview, together with a new id. Pass that new id back into `image_ids` to keep iterating (e.g. to make another change on top of this one).

The image shown inline is a downscaled preview so it stays cheap to send back and forth; the full-resolution original is kept in storage regardless. The user can save that original to disk at any time with the "Export Image" command.

Parameters:
- `prompt` (required): a text description of how to edit or revise the source image(s).
- `image_ids` (optional): ids of previously generated or edited images to use as sources.
- `file_paths` (optional): paths to image files on disk to use as sources, resolved relative to the working directory.
- `size` (optional): the requested output size, e.g. "1024x1024". Leave unset to let the model choose.
- `quality` (optional): the requested rendering quality, e.g. "high", "medium", "low", "auto". Leave unset to let the model choose.
- `background` (optional): "transparent", "opaque", or "auto". Use "transparent" for an image meant to be composited over other content, such as an icon or sticker.

This calls a billed, external image-generation API and requires network access.
