import { describe, expect, it } from "vitest";
import { ATTACHMENT_MAX_BYTES, ATTACHMENT_MESSAGES, attachmentProblem } from "./attachments";

describe("attachmentProblem", () => {
  it("accepts photos and PDFs up to 10 MB", () => {
    for (const type of ["image/jpeg", "image/png", "image/webp", "application/pdf"]) {
      expect(attachmentProblem({ type, size: ATTACHMENT_MAX_BYTES })).toBeNull();
    }
  });

  it("refuses other types before the size", () => {
    expect(attachmentProblem({ type: "image/gif", size: ATTACHMENT_MAX_BYTES + 1 })).toBe(ATTACHMENT_MESSAGES.type);
  });

  it("refuses empty and oversized files", () => {
    expect(attachmentProblem({ type: "image/png", size: 0 })).toBe(ATTACHMENT_MESSAGES.size);
    expect(attachmentProblem({ type: "image/png", size: ATTACHMENT_MAX_BYTES + 1 })).toBe(ATTACHMENT_MESSAGES.size);
  });
});
