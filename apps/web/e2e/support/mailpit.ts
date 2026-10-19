import { expect, type APIRequestContext } from "@playwright/test";

// Reads the dev mail catcher (Mailpit, docker project "tally") so journeys can follow the links the
// API mails: verification, password reset. E2E_MAILPIT_URL overrides the default address.

export const MAILPIT_URL = process.env.E2E_MAILPIT_URL ?? "http://localhost:8025";

interface MessageSummary {
  ID: string;
  Created: string;
  Subject: string;
}

/**
 * Waits for a mail to `to` created after `since` whose text contains a link to `pathname`, and
 * returns that link.
 */
export async function waitForMailLink(request: APIRequestContext, to: string, pathname: string, since: Date): Promise<URL> {
  let link: URL | null = null;
  await expect
    .poll(
      async () => {
        const res = await request.get(`${MAILPIT_URL}/api/v1/search`, { params: { query: `to:"${to}"` } });
        if (!res.ok()) return `mailpit answered ${res.status()}`;
        const { messages } = (await res.json()) as { messages: MessageSummary[] | null };
        for (const m of messages ?? []) {
          if (new Date(m.Created).getTime() < since.getTime()) continue;
          const full = await request.get(`${MAILPIT_URL}/api/v1/message/${m.ID}`);
          if (!full.ok()) continue;
          const { Text, HTML } = (await full.json()) as { Text?: string; HTML?: string };
          for (const raw of `${Text ?? ""}\n${HTML ?? ""}`.match(/https?:\/\/[^\s"'<>]+/g) ?? []) {
            const url = new URL(raw.replace(/&amp;/g, "&"));
            if (url.pathname === pathname) {
              link = url;
              return "found";
            }
          }
        }
        return "waiting";
      },
      { message: `mail to ${to} with a ${pathname} link`, timeout: 20_000, intervals: [500, 1000, 2000] },
    )
    .toBe("found");
  if (!link) throw new Error("unreachable: mail link not captured");
  return link;
}
