import { describe, expect, it, vi } from "vitest";
import { BEEPS, VIBRATION, createFeedback } from "./feedback";

/** An AudioContext that records the tones started on it. */
function fakeAudio(state: AudioContextState = "running") {
  const tones: Array<{ hz: number; peak: number }> = [];
  const resume = vi.fn(() => Promise.resolve());
  const ctx = {
    state,
    currentTime: 0,
    destination: {},
    resume,
    createOscillator() {
      let hz = 0;
      const osc = {
        type: "sine",
        frequency: {
          setValueAtTime: (v: number) => {
            hz = v;
          },
        },
        connect: (node: unknown) => node,
        start: () => tones.push({ hz, peak: gainPeak }),
        stop: () => undefined,
      };
      return osc;
    },
    createGain() {
      return {
        gain: {
          setValueAtTime: () => undefined,
          exponentialRampToValueAtTime: (v: number) => {
            if (v > 0.001) gainPeak = v;
          },
        },
        connect: (node: unknown) => node,
      };
    },
  };
  let gainPeak = 0;
  return { create: vi.fn(() => ctx as unknown as AudioContext), tones, resume };
}

describe("createFeedback", () => {
  it("vibrates on a phone that can, and neither beeps nor flashes", () => {
    const vibrate = vi.fn(() => true);
    const audio = fakeAudio();
    const flash = vi.fn();
    const feedback = createFeedback({ vibrate, createAudioContext: audio.create, flash });
    feedback.signal("ok");
    feedback.signal("error");
    expect(vibrate.mock.calls).toEqual([[VIBRATION.ok], [VIBRATION.error]]);
    expect(audio.tones).toEqual([]);
    expect(flash).not.toHaveBeenCalled();
  });

  it("beeps high or low and flashes where vibration does not exist (iPhone)", () => {
    const audio = fakeAudio();
    const flash = vi.fn();
    const feedback = createFeedback({ vibrate: null, createAudioContext: audio.create, flash });
    feedback.signal("ok");
    feedback.signal("error");
    expect(audio.tones.map((t) => t.hz)).toEqual([BEEPS.ok.hz, BEEPS.error.hz]);
    expect(flash.mock.calls).toEqual([["ok"], ["error"]]);
  });

  it("beeps and flashes when the phone refuses to vibrate", () => {
    const audio = fakeAudio();
    const flash = vi.fn();
    createFeedback({ vibrate: () => false, createAudioContext: audio.create, flash }).signal("error");
    expect(audio.tones.map((t) => t.hz)).toEqual([BEEPS.error.hz]);
    expect(flash).toHaveBeenCalledWith("error");
  });

  it("unlocks audio inside the tap with a silent tone and resumes a suspended context", () => {
    const audio = fakeAudio("suspended");
    const feedback = createFeedback({ vibrate: null, createAudioContext: audio.create, flash: vi.fn() });
    feedback.unlock();
    expect(audio.create).toHaveBeenCalledTimes(1);
    expect(audio.resume).toHaveBeenCalled();
    expect(audio.tones).toHaveLength(1);
    expect(audio.tones[0]?.peak).toBe(0);
    feedback.signal("ok");
    // The same context is used again.
    expect(audio.create).toHaveBeenCalledTimes(1);
  });

  it("still flashes when audio is unavailable or broken", () => {
    const flash = vi.fn();
    createFeedback({ vibrate: null, createAudioContext: null, flash }).signal("ok");
    createFeedback({
      vibrate: null,
      createAudioContext: () => {
        throw new Error("blocked");
      },
      flash,
    }).signal("error");
    expect(flash.mock.calls).toEqual([["ok"], ["error"]]);
  });
});
