Generates a new image from a text prompt.

Returns the image inline in the conversation as a preview, together with an id like `img_1a2b3c4d5e6f`. Keep that id: pass it in ImageEdit's `image_ids` parameter to revise this exact image (change a detail, adjust the style, and so on) instead of writing a new prompt from scratch.

The image shown inline is a downscaled preview so it stays cheap to send back and forth; the full-resolution original is kept in storage regardless. The user can save that original to disk at any time with the "Export Image" command.

Parameters:
- `prompt` (required): a text description of the image to generate.
- `size` (optional): the requested output size, e.g. "1024x1024". Leave unset to let the model choose.
- `quality` (optional): the requested rendering quality, e.g. "high", "medium", "low", "auto". Leave unset to let the model choose.
- `background` (optional): "transparent", "opaque", or "auto". Use "transparent" for an image meant to be composited over other content, such as an icon or sticker.

This calls a billed, external image-generation API and requires network access.
