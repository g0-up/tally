import "fake-indexeddb/auto";
import { deleteDB } from "idb";
import { OFFLINE_DB_NAME, closeOfflineDb } from "../offline-db";

// Importing this module installs an in-memory IndexedDB (fake-indexeddb) for the test file.

/** Closes the page's connection and deletes the database: each test starts from an empty device. */
export async function resetOfflineDb(): Promise<void> {
  await closeOfflineDb();
  await deleteDB(OFFLINE_DB_NAME);
}
