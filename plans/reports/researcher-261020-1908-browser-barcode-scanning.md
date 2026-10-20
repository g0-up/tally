# Nghiên cứu: quét mã vạch trên trình duyệt điện thoại và sinh QR trên máy tính

Ngày: 2026-10-20 · Phạm vi: PWA React 19 + Vite 8 (`apps/web`), trang quét công khai trên Chrome Android và Safari iOS
(2 bản gần nhất), trang máy tính hiện QR ghép đôi. Bối cảnh lấy từ
`plans/reports/brainstorm-261020-1826-phone-barcode-scanning.md`.

## Kết luận

1. **Chọn `barcode-detector@3.2.3` (ponyfill, MIT) kèm `zxing-wasm@3.1.6` làm dependency trực tiếp, ghim chính xác.**
   Dùng `BarcodeDetector` gốc khi `getSupportedFormats()` có đủ 6 định dạng, nếu không thì nạp lười ponyfill. Ponyfill
   có cùng interface với API gốc, nên chỉ cần một vòng quét. `.wasm` tự host qua `?url` của Vite và
   `prepareZXingModule({ overrides: { locateFile } })`. Đã build thử: không có gì của zxing lọt vào bundle chính.
2. **iOS Safari vẫn chưa có `BarcodeDetector`.** API nằm sau cờ tắt mặc định trên iOS 17 đến 27.2, và từ iOS 18 bật cờ
   cũng không chạy. Lưu ý không có "iOS 19": sau iOS 18 Apple chuyển sang iOS 26 (2025) rồi iOS 27 (tháng 9/2026). Vì vậy
   "2 bản gần nhất" là 26.x và 27.x, và mọi iPhone sẽ đi đường WASM.
3. **iOS không rung được.** `navigator.vibrate` không có trên Safari iOS ở mọi phiên bản. Tiêu chí "điện thoại rung" của
   brainstorm cần sửa thành: rung trên Android, còn iOS dùng tiếng bíp (Web Audio) cộng với nháy màn hình.
4. **Hạ tầng cần đổi:** `script-src 'self' 'wasm-unsafe-eval'` (CSP hiện tại không cho chạy WASM), `camera=(self)`, thêm
   `globIgnores` cho chunk/worker quét. Không cần `worker-src blob:`.
5. **QR trên máy tính: `uqr@0.1.3`** (MIT, không dependency, khoảng 4 KB gzip, có `renderSVG`/`encode`).
6. **Phát hiện quan trọng ngoài câu hỏi:** zxing-cpp 3.x trả UPC-A/UPC-E dưới dạng **13 chữ số**, nên trên iOS UPC-A
   `036000291452` thành `0036000291452`. Nếu không chuẩn hóa GTIN thì cùng một mã sẽ khớp trên Android nhưng trượt trên
   iPhone (chi tiết ở mục 8).

---

## 1. Hỗ trợ `BarcodeDetector` (tháng 10/2026)

| Nền tảng | Trạng thái | Nguồn |
|---|---|---|
| Chrome Android | Có từ Chrome 83. Cần Google Play Services ≥ 19.7.42, thiếu thì provider trả `null` | [caniuse](https://caniuse.com/mdn-api_barcodedetector), [Chromium `BarcodeDetectionProviderImpl.java`](https://chromium.googlesource.com/chromium/src/+/main/services/shape_detection/android/java/src/org/chromium/shape_detection/BarcodeDetectionProviderImpl.java), [Chrome docs](https://developer.chrome.com/docs/capabilities/shape-detection) |
| Định dạng trên Android | `aztec, code_128, code_39, code_93, codabar, data_matrix, ean_13, ean_8, itf, pdf417, qr_code, upc_a, upc_e` (đủ 6 định dạng cần dùng) | [Chromium source](https://chromium.googlesource.com/chromium/src/+/main/services/shape_detection/android/java/src/org/chromium/shape_detection/BarcodeDetectionProviderImpl.java) |
| Safari iOS 17 đến 27.2 | "Disabled by default" (cờ *Shape Detection API*) | [caniuse](https://caniuse.com/mdn-api_barcodedetector), MDN BCD 8.1.6 (`api.BarcodeDetector`: `safari_ios 17, flags`) |
| Safari iOS 18 trở lên khi bật cờ | Không phát hiện được gì (lỗi hồi quy, chưa có bản sửa nào được ghi nhận) | [WebKit bug 281848](https://bugs.webkit.org/show_bug.cgi?id=281848), [Apple forum 767761](https://developer.apple.com/forums/thread/767761) |
| Safari 27.0 | Ghi chú phát hành không nhắc tới BarcodeDetector hay Shape Detection | [WebKit Features for Safari 27.0](https://webkit.org/blog/18325/webkit-features-for-safari-27-0/) |
| Chrome desktop | Chỉ có trên macOS/ChromeOS | [Chrome docs](https://developer.chrome.com/docs/capabilities/shape-detection) |

Phiên bản iOS: caniuse liệt kê Safari iOS 26.x và 27.0 đến 27.2. Safari 27 ra cùng iOS 27 ngày 18/9/2026
([iphonesoft](https://iphonesoft.fr/2026/09/18/safari-27-navigateur-apple-se-ia-serveur-mcp)).

**Phát hiện tính năng.** Chỉ kiểm tra `'BarcodeDetector' in window` là chưa đủ: Chrome nói rõ "the presence of an
interface doesn't tell you whether the underlying platform supports the feature"
([Chrome docs](https://developer.chrome.com/docs/capabilities/shape-detection)). Trên Android thiếu Play Services,
interface vẫn có nhưng danh sách định dạng rỗng. Vì vậy phải kiểm tra từng định dạng:

```ts
const FORMATS = ["ean_13", "ean_8", "upc_a", "upc_e", "code_128", "qr_code"] as const;

export async function createDetector(): Promise<BarcodeDetectorLike> {
  const Native = (globalThis as { BarcodeDetector?: BarcodeDetectorCtor }).BarcodeDetector;
  const supported = Native ? await Native.getSupportedFormats().catch(() => []) : [];
  if (Native && FORMATS.every((f) => supported.includes(f))) return new Native({ formats: [...FORMATS] });
  const { BarcodeDetector } = await import("./barcode-fallback"); // chunk lười, xem mục 2
  return new BarcodeDetector({ formats: [...FORMATS] });
}
```

`BarcodeDetector` chỉ có trong secure context và có trong Web Worker
([MDN](https://developer.mozilla.org/en-US/docs/Web/API/Barcode_Detection_API)).

## 2. So sánh thư viện dự phòng

Phiên bản và ngày lấy trực tiếp từ `registry.npmjs.org`, trạng thái repo lấy từ GitHub API (20/10/2026). Kích thước đo
bằng Vite 8.3.4 trong scratchpad (build ứng dụng có minify, gzip -9).

| Thư viện | Bản mới nhất | JS min / gzip | WASM | Nạp `.wasm` | License | Bảo trì | Độ chính xác EAN/Code128 qua camera |
|---|---|---|---|---|---|---|---|
| **`barcode-detector`** (Sec-ant) | 3.2.3 (19/10/2026) | 43.1 KB / 14.7 KB | `zxing_reader.wasm` 947 KB, gzip 410 KB | Mặc định qua jsDelivr. Tự host bằng `prepareZXingModule({ overrides: { locateFile } })`. `setZXingModuleOverrides` đã deprecated | MIT (wasm: Apache-2.0 từ zxing-cpp) | Hoạt động: phát hành hằng tháng, push 19/10/2026 | Engine zxing-cpp. Upstream tuyên bố "many improvements in terms of runtime and detection performance" so với ZXing Java ([zxing-cpp README](https://github.com/zxing-cpp/zxing-cpp)) |
| `zxing-wasm` (dùng trực tiếp) | 3.1.6 (19/10/2026) | 35.9 KB / 12.6 KB (`/reader`) | Như trên | Như trên, có thêm `wasmBinary`/`instantiateWasm` | MIT + Apache-2.0 | Như trên | Như trên. Có thêm `ReaderOptions` (`tryHarder`, `tryDownscale`, `maxNumberOfSymbols`…) |
| `@zxing/browser` + `@zxing/library` | 0.2.1 / 0.23.0 | 477 KB / 123 KB | Không (JS thuần) | Không áp dụng | MIT / Apache-2.0 | README ghi "Project in Maintenance Mode Only… open to new maintainers" ([zxing-js/library](https://github.com/zxing-js/library)) | Bỏ sót Code 128 nét mảnh ở độ phân giải thấp ([STRICH](https://strich.io/comparison-with-oss.html), bài so sánh của nhà bán) |
| `html5-qrcode` | 2.3.8 (15/4/2023) | 369 KB / 107 KB | Không (bọc ZXing-JS) | Không áp dụng | Apache-2.0 | README: "in maintenance mode… Pull requests also won't be merged" ([repo](https://github.com/mebjas/html5-qrcode)) | Kế thừa ZXing-JS. Gặp vấn đề với mã đảo màu hoặc tương phản thấp ([Scanbot](https://scanbot.io/blog/quagga2-vs-html5-qrcode-scanner/), bài của nhà bán) |
| `@ericblade/quagga2` | 1.12.1 (20/12/2025) | 154 KB / 42 KB | Không | Không áp dụng | MIT | Hoạt động (push 19/10/2026) | Chỉ đọc 1D. QR phải cài plugin ngoài ([README](https://github.com/ericblade/quagga2)). Đọc sai hoặc bỏ sót khi bản in kém ([STRICH](https://strich.io/comparison-with-oss.html)) |

Bằng chứng độ chính xác định lượng duy nhất cho zxing-wasm là benchmark của Dynamsoft trên 7.894 ảnh BarBeR: recall
67,25%, precision 92,35%, median 74 ms. Đây là bài của nhà bán và so với sản phẩm của chính họ
([Dynamsoft](https://www.dynamsoft.com/codepool/benchmark-barcode-reading-javascript-zxing-wasm-dynamsoft-barcode-reader.html)).
Chưa có benchmark độc lập so sánh cả 5 thư viện trên EAN-13. Cần tự đo trên máy thật (xem mục Câu hỏi chưa giải quyết).

**Xếp hạng:**
1. **`barcode-detector@3.2.3`.** Interface giống hệt API gốc, nên chỉ cần một vòng quét cho cả hai đường. Engine tốt
   nhất trong nhóm mã nguồn mở. JS nhẹ nhất trong các lựa chọn đọc được QR. Bản 3.2.3 sửa rò rỉ bộ nhớ WASM khi gọi
   `readBarcodes` liên tục và giảm độ trễ khoảng 40–48% (đo ở 720p/1080p)
   ([release 3.2.3](https://github.com/Sec-ant/barcode-detector/releases), [zxing-wasm 3.1.6](https://github.com/Sec-ant/zxing-wasm/releases)).
   Với vòng quét liên tục, bản sửa rò rỉ này là lý do chọn bản mới nhất thay cho 3.2.2.
2. `zxing-wasm@3.1.6` dùng trực tiếp. Nhỏ hơn khoảng 2 KB gzip và chỉnh được `tryHarder`/`tryDownscale`, nhưng phải tự
   viết lớp chuyển đổi sang interface của API gốc (trùng với việc ponyfill đã làm). Chỉ nên chọn khi đo thấy ponyfill
   quá chậm trên iPhone đời cũ.
3. Loại `@zxing/browser` và `html5-qrcode`: đang ở chế độ bảo trì và nặng gấp khoảng 8 lần. Loại `quagga2`: không đọc
   được QR.

**Rủi ro áp dụng:** 3.2.3 mới phát hành 1 ngày và có nâng Emscripten lên 6.0.12. Changelog ghi "retaining support for
existing browser targets". Nếu QA trên iOS 26 gặp lỗi, lùi về cặp `barcode-detector@3.2.2` + `zxing-wasm@3.1.3`. Cả hai
repo chỉ có một maintainer chính (Sec-ant), nên vẫn có rủi ro bị bỏ rơi. Nếu chuyện đó xảy ra, engine zxing-cpp vẫn được
duy trì (push 6/10/2026), và có thể build lại wasm hoặc chuyển sang thư viện bọc khác.

### Cấu hình tự host `.wasm` (đã kiểm chứng bằng build Vite 8.3.4)

```bash
pnpm add -E barcode-detector@3.2.3 zxing-wasm@3.1.6
```

`zxing-wasm` phải là dependency trực tiếp. Lý do: pnpm không hoist dependency bắc cầu, nên lệnh import
`zxing-wasm/reader/zxing_reader.wasm` sẽ không resolve được. Gói này export sẵn subpath `./reader/zxing_reader.wasm`.
Phiên bản phải **trùng chính xác** với bản mà `barcode-detector` khai báo (`"zxing-wasm": "3.1.6"`), vì "Each version of
this library has a unique corresponding `.wasm` file"
([zxing-wasm README](https://github.com/Sec-ant/zxing-wasm#configuring-wasm-serving)). Có thể thêm một unit test so
sánh `ZXING_WASM_SHA256` với SHA-256 của file để bắt lệch phiên bản khi nâng cấp. Trong scratchpad, `sha match true`.

```ts
// src/features/scan/barcode-fallback.ts: chỉ được import động
import { BarcodeDetector, prepareZXingModule } from "barcode-detector/ponyfill";
import wasmUrl from "zxing-wasm/reader/zxing_reader.wasm?url";

prepareZXingModule({
  overrides: { locateFile: (path, prefix) => (path.endsWith(".wasm") ? wasmUrl : prefix + path) },
});
export { BarcodeDetector };
```

- `?url` cho Vite phát ra asset có hash (`assets/zxing_reader-<hash>.wasm`)
  ([Vite assets](https://vite.dev/guide/assets)). Build thử cho ra đúng 1 file wasm, và chunk fallback tham chiếu tới
  `/assets/zxing_reader-BCjuyud6.wasm`.
- Wasm chỉ được tải ở lần `detect()` đầu tiên. Muốn tải trước khi người dùng bấm "Bật camera" thì truyền
  `fireImmediately: true` ([zxing-wasm README](https://github.com/Sec-ant/zxing-wasm#controlling-wasm-instantiation-timing-and-caching)).
- Glue Emscripten dùng `WebAssembly.instantiateStreaming`, nên server phải trả `Content-Type: application/wasm`. Nếu
  không, nó tụt về nhánh ArrayBuffer chậm hơn. `nginx-unprivileged:1.27.5` đã có `application/wasm wasm;` trong
  [`mime.types`](https://raw.githubusercontent.com/nginx/nginx/release-1.27.5/conf/mime.types). Nên thêm
  `application/wasm` vào `gzip_types` trong `apps/web/nginx.conf` (947 KB còn 410 KB). Cloudflare cũng liệt kê
  `application/wasm` trong danh sách nén
  ([Cloudflare](https://developers.cloudflare.com/speed/optimization/content/compression/)).
- Kiểm thử round-trip trong Node với wasm tự host: EAN-13, EAN-8, UPC-A, UPC-E, Code128 và QR đều giải đúng (riêng UPC
  có dạng 13 chữ số, xem mục 8).

## 3. Camera

**Ràng buộc gợi ý:**

```ts
const stream = await navigator.mediaDevices.getUserMedia({
  audio: false,
  video: {
    facingMode: { ideal: "environment" },   // dùng ideal: máy chỉ có 1 camera vẫn chạy, không ném OverconstrainedError
    width: { ideal: 1920 }, height: { ideal: 1080 },
    advanced: [{ focusMode: "continuous" } as MediaTrackConstraintSet], // Chrome Android có, iOS bỏ qua
  },
});
```

- `facingMode`: giá trị thường là "ideal", còn `{ exact: "environment" }` là bắt buộc
  ([MDN getUserMedia](https://developer.mozilla.org/en-US/docs/Web/API/MediaDevices/getUserMedia)). Dùng `ideal` để tránh
  `OverconstrainedError` trên máy tính bảng hoặc thiết bị lạ.
- Độ phân giải: ML Kit khuyên 1280x720 hoặc 1920x1080. EAN-13 rộng 95 module nên cần **≥ 190 px** trong ảnh
  ([ML Kit](https://developers.google.com/ml-kit/vision/barcode-scanning/ios)). STRICH mặc định 720p và chỉ tăng khi mã
  nhỏ hoặc mờ ([STRICH docs](https://docs.strich.io/reference/interfaces/FrameSourceConfiguration.html)). Gợi ý: xin
  1080p, rồi ở đường WASM cắt một dải ROI giữa khung (khoảng 70% chiều rộng) để giảm số pixel mà vẫn giữ EAN ≥ 190 px.
- `focusMode`/`torch`/`zoom` thuộc Image Capture spec. Chúng chỉ được ràng buộc dạng basic tùy chọn hoặc `advanced`,
  không được ở dạng bắt buộc ([W3C mediacapture-image](https://w3c.github.io/mediacapture-image/)). WebKit **chưa** hỗ
  trợ `focusMode` (`// FIXME: add focusMode` trong
  [MediaTrackSupportedConstraints.idl](https://github.com/WebKit/WebKit/blob/main/Source/WebCore/Modules/mediastream/MediaTrackSupportedConstraints.idl)),
  nhưng có `zoom` và `torch`.
- **Đèn pin:** đọc `track.getCapabilities().torch` (chỉ có sau khi luồng đã chạy,
  [Chrome blog](https://developer.chrome.com/blog/imagecapture)), rồi gọi
  `track.applyConstraints({ advanced: [{ torch: true }] })`. iOS có torch từ khoảng 17.x
  ([WebKit bug 243075](https://bugs.webkit.org/show_bug.cgi?id=243075)). Safari 18.4 sửa lỗi `getSettings()` trả giá
  trị torch cũ ([Safari 18.4](https://webkit.org/blog/16574/webkit-features-in-safari-18-4/)). Có báo cáo cho thấy
  `getCapabilities().torch` trên iOS không đáng tin
  ([ví dụ](https://github.com/SamandarAlimov/socialalsamos/pull/218)). Nên hiện nút đèn pin khi `torch` có trong
  capabilities, và xử lý lỗi khi áp dụng.
- **iPhone Pro lấy nét gần kém:** camera "Back" mặc định là ống góc rộng, khoảng cách lấy nét tối thiểu lớn, nên mã ở
  10–12 cm bị mờ ([Apple forum 715568](https://developer.apple.com/forums/thread/715568?page=2),
  [730122](https://developer.apple.com/forums/thread/730122)). Cách giảm: ghi hướng dẫn "giữ cách 15–20 cm", và nếu
  `getCapabilities().zoom` có thì đặt sẵn zoom khoảng 1.5–2.

**Quirk iOS Safari:**
- Phần tử `<video>` phải có `playsinline muted autoplay`. Thiếu `playsinline` thì iPhone bắt phát toàn màn hình. Video
  không có audio được tự phát mà không cần cử chỉ người dùng ([WebKit video policies](https://webkit.org/blog/6784/)).
  Đây cũng là cách làm với getUserMedia từ iOS 11 ([WebKit bug 176843](https://bugs.webkit.org/show_bug.cgi?id=176843)).
  Gán `video.srcObject = stream` rồi `await video.play()`.
- Cử chỉ người dùng: không tìm thấy tài liệu chính thức nói getUserMedia bắt buộc phải có cử chỉ. Dù vậy vẫn nên mở
  camera từ nút "Bật camera". Có ba lý do: lời nhắc quyền không xuất hiện bất ngờ, `play()` không bị từ chối, và trang
  có *sticky activation* để sau này `navigator.vibrate` trên Chrome và `AudioContext.resume()` trên iOS chạy được
  (mục 4).
- **Hỏi lại quyền:** Safari iOS thường hỏi lại quyền camera ở mỗi lần truy cập hoặc khi điều hướng
  ([Apple forum 788518](https://developer.apple.com/forums/thread/788518),
  [Apple Community](https://discussions.apple.com/thread/256081579)). Cách khắc phục duy nhất phía người dùng là
  "AA → Website Settings → Camera: Allow"
  ([Scandit](https://support.scandit.com/hc/en-us/articles/360008443011-Why-does-iOS-keep-asking-for-camera-permissions)).
  Nên đưa hướng dẫn này vào trang quét. Đừng đổi URL (kể cả hash) trong lúc camera đang chạy.
- **PWA standalone:** getUserMedia chạy trong web app trên Home Screen từ iOS 13.4
  ([WebKit bug 185448](https://bugs.webkit.org/show_bug.cgi?id=185448)). Có báo cáo bị hỏi lại quyền và mất luồng khi
  đổi route hash ([WebKit bug 215884](https://bugs.webkit.org/show_bug.cgi?id=215884)), và video bị xoay 90° trên iOS 26
  ([Apple forum 801146](https://developer.apple.com/forums/thread/801146)). Safari 27 có sửa lỗi "transient device
  rotation… wrong orientation" ([Safari 27](https://webkit.org/blog/18325/webkit-features-for-safari-27-0/)). Trang
  quét được mở bằng cách quét QR trong app Camera, nên nó chạy trong tab Safari chứ không ở chế độ standalone. Các lỗi
  standalone chỉ gặp khi người dùng tự cài app lên màn hình chính của điện thoại.
- **HTTPS:** ngoài secure context thì `navigator.mediaDevices` là `undefined`
  ([MDN](https://developer.mozilla.org/en-US/docs/Web/API/MediaDevices/getUserMedia)).
- **Permissions-Policy:** đổi `camera=()` thành `camera=(self)`. Allowlist mặc định của camera là `self`, và khi bị
  chính sách chặn thì getUserMedia trả `NotAllowedError`
  ([MDN camera](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Permissions-Policy/camera)). Safari
  không hỗ trợ header `Permissions-Policy` (BCD: `safari_ios false`). Vì vậy cấu hình hiện tại chỉ chặn Chrome Android,
  và không thể kiểm thử thay đổi này bằng iPhone.
- Lỗi cần xử lý và hiển thị bằng tiếng Việt: `NotAllowedError`, `NotFoundError`, `NotReadableError` (camera đang bị app
  khác dùng), `OverconstrainedError` ([MDN](https://developer.mozilla.org/en-US/docs/Web/API/MediaDevices/getUserMedia)).
- Dừng track (`track.stop()`) ở `pagehide`, và tạm dừng vòng quét khi `document.hidden`. Nếu không, đèn báo camera
  vẫn sáng và pin bị hao.

## 4. Vòng quét, luồng phụ, chống lặp và phản hồi

**`requestVideoFrameCallback` thay cho `setInterval`.** rVFC chỉ chạy khi có khung hình mới, với tần suất bằng giá trị
nhỏ hơn giữa fps của video và tần số màn hình ([web.dev](https://web.dev/articles/requestvideoframecallback-rvfc)). Hỗ
trợ: Chrome 83, Safari 15.4 (cả hai nền tảng mục tiêu đều có, nên không cần polyfill). Cách giới hạn tần suất:

```ts
const MIN_GAP_MS = 125; // khoảng 8 fps
let busy = false, last = 0;
const tick = async (now: DOMHighResTimeStamp) => {
  if (!busy && now - last >= MIN_GAP_MS && !document.hidden) {
    busy = true; last = now;
    try { onResults(await detector.detect(sourceFor(video))); } finally { busy = false; }
  }
  rvfcId = video.requestVideoFrameCallback(tick); // lưu id để cancelVideoFrameCallback khi unmount
};
```

Cờ `busy` tạo backpressure: máy chậm tự giảm fps, không bị dồn hàng đợi. Đường gốc (Chrome) chỉ cần
`detector.detect(video)` trên luồng chính, vì `detect` là bất đồng bộ và trình duyệt tự xử lý ngoài luồng chính.

**Giải mã ngoài luồng chính (đường WASM).** iOS hỗ trợ đủ cho các bản mục tiêu:
- `OffscreenCanvas` 2D trong worker: Safari 16.4 ([Safari 16.4](https://webkit.org/blog/13966/webkit-features-in-safari-16-4/)).
- `createImageBitmap` (có tham số cắt vùng): Safari 15. `DOMRectReadOnly` trong worker: Safari 10.3 (MDN BCD 8.1.6).
- Ponyfill tự dùng `OffscreenCanvas` khi có (đã đọc mã nguồn `zxing-exported.js`), nên `detect(ImageBitmap)` chạy được
  trong worker.

Mẫu: luồng chính gọi `createImageBitmap(video, sx, sy, sw, sh)` (cắt ROI), rồi `worker.postMessage(bmp, [bmp])`.
Worker gọi `detector.detect(bmp)`, sau đó `bmp.close()`, và trả về `{format, rawValue}[]`. Vite:
`new Worker(new URL("./barcode.worker.ts", import.meta.url), { type: "module" })`. Đã build thử: worker thành
`assets/barcode.worker-<hash>.js` (43.5 KB), chứa ponyfill và tham chiếu tới file wasm có hash. Mặc định Vite build
worker dạng `iife` và chạy tốt. Đường gốc không cần worker. Nếu muốn giữ đơn giản ở bản đầu, có thể chạy ponyfill trên
luồng chính với ROI và khoảng 5 fps. Trade-off là UI có thể giật khi mỗi lần giải mã mất vài chục ms (median 74 ms trong
benchmark Dynamsoft, chưa tính bản tăng tốc 3.2.3).

**Chống lặp.** Dùng cửa sổ trượt theo lần *nhìn thấy* cuối, không theo lần *gửi* cuối. Giữ mã trong khung thì không bị
gửi lặp, còn đưa mã ra rồi vào lại thì được tính là lần quét mới (cần cho việc quét 2 lần để cộng số lượng):

```ts
const ABSENT_MS = 800;
let lastCode = "", lastSeen = 0;
function accept(code: string, now: number): boolean {
  const isNew = code !== lastCode || now - lastSeen > ABSENT_MS;
  lastCode = code; lastSeen = now;
  return isNew;
}
```

Khóa idempotency của mỗi lần gửi (theo brainstorm) vẫn cần thêm, để xử lý việc gửi lại trên mạng.

**Phản hồi xúc giác:**
- `navigator.vibrate`: Chrome Android có. Từ Chrome 60, gọi khi chưa có cử chỉ người dùng sẽ trả `false`. Safari iOS và
  macOS không có ở mọi phiên bản (MDN BCD 8.1.6, [caniuse](https://caniuse.com/vibration)). Safari 27 cũng không bổ sung
  ([Safari 27](https://webkit.org/blog/18325/webkit-features-for-safari-27-0/)).
- Dự phòng: (1) tiếng bíp ngắn bằng Web Audio (`AudioContext` có trên iOS từ 14.5). Tạo hoặc `resume()` context trong
  handler của nút "Bật camera", vì iOS cần cử chỉ để mở âm thanh. Có thể đặt
  `navigator.audioSession.type = "transient"` (iOS 16.4+), loại dành cho âm báo ngắn và phát đè lên âm thanh khác
  ([MDN AudioSession.type](https://developer.mozilla.org/en-US/docs/Web/API/AudioSession/type)). (2) Nháy viền xanh hoặc
  đỏ kèm kết quả trên màn hình. Đây là kênh duy nhất chắc chắn chạy, vì công tắc im lặng hoặc âm lượng có thể tắt
  tiếng bíp.
- Không dùng mẹo `<input type="checkbox" switch>` để tạo haptic trên iOS. Đây là hành vi không được tài liệu hóa, có đề
  xuất bắt buộc cử chỉ người dùng ([WebKit bug 285120](https://bugs.webkit.org/show_bug.cgi?id=285120)), và có nguồn
  (chưa kiểm chứng) cho rằng iOS 26.5 đã chặn
  ([ios-haptics](https://npmjs.com/package/ios-haptics), [tester](https://rapidtoolset.com/en/tool/ios-haptics-tester)).

## 5. Sinh QR trên máy tính

| Thư viện | Bản | min / gzip (đo) | SVG | Ghi chú |
|---|---|---|---|---|
| **`uqr`** | 0.1.3 (3/4/2026) | 10.6 KB / 4.1 KB | `renderSVG()` trả chuỗi, `encode()` trả `boolean[][]` | unjs, MIT, 0 dependency, thuật toán từ Nayuki ([README](https://github.com/unjs/uqr)) |
| `lean-qr` | 2.7.4 (7/9/2026) | 7.7 KB / 4.1 KB | `lean-qr/extras/svg` | MIT, nhỏ nhất, nhưng cộng đồng rất nhỏ (61 sao GitHub) |
| `qr` (paulmillr) | 0.7.2 (28/9/2026) | 14.4 KB / 6.3 KB | `encodeQR(text,"svg")` | MIT/Apache, hoạt động. Còn có module decode mà ta không cần |
| `qrcode-generator` | 2.0.4 (7/8/2025) | 20.7 KB / 7.3 KB | `createSvgTag()` | MIT, API cũ |
| `qrcode` | 1.5.4 (5/8/2024) | 24.1 KB / 9.1 KB | `toString({type:"svg"})` | Có dependency Node (`pngjs`, `yargs`). Repo không push từ 8/2024 |

**Chọn `uqr@0.1.3` (`pnpm add -E uqr@0.1.3`).** Nó nhỏ, có API SVG trực tiếp, không dependency, và do một tổ chức uy
tín duy trì. Rủi ro: 0.1.x và từng 2,5 năm không phát hành. Chấp nhận được vì QR là thuật toán đã ổn định. Dùng
`encode(url, { ecc: "M", border: 2 }).data` để tự vẽ `<svg>`/`<path>` bằng React, tránh `dangerouslySetInnerHTML`.
Hoặc dùng `renderSVG` (chuỗi do thư viện sinh từ dữ liệu của ta). Inline SVG không bị CSP hiện tại chặn. Import trong
component khung QR, vốn đã nằm trong các route POS, Kiểm kê và Nhập hàng.

## 6. CSP

CSP production hiện tại (`infra/docker-compose.homelab.yml:33`) không có `script-src`, nên `default-src 'self'` áp cho
script.
- **Cần `'wasm-unsafe-eval'`** cho đường ponyfill: "If a page has a CSP header and `'wasm-unsafe-eval'` isn't specified
  in the `script-src` directive, WebAssembly is blocked from loading and executing"
  ([MDN script-src](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Content-Security-Policy/script-src)).
  Thêm `script-src 'self' 'wasm-unsafe-eval'`. Hỗ trợ: Safari iOS 16, Chrome 97
  ([caniuse](https://caniuse.com/mdn-http_headers_content-security-policy_script-src_wasm-unsafe-eval)). Từ khóa này
  hẹp hơn `'unsafe-eval'` vì không mở `eval()` của JS. Đường gốc của Chrome không cần.
  - Áp toàn site là đơn giản nhất. Muốn chặt hơn thì dùng router Traefik riêng theo `PathPrefix` của trang quét, gắn
    middleware header thứ hai. Phương án này không cần thiết cho mô hình đe dọa hiện tại.
- **Không cần `worker-src blob:`.** `worker-src` lần lượt fallback về `child-src`, `script-src`, rồi `default-src`
  ([MDN worker-src](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Content-Security-Policy/worker-src)).
  Worker của Vite là file cùng origin, và zxing-wasm/barcode-detector không tạo worker hay blob (đã grep: không có
  `new Worker`, `eval`, `new Function`). Chỉ cần `blob:` nếu dùng `?worker&inline`.
- `connect-src 'self'` đã đủ cho `fetch` file `.wasm` tự host. URL jsDelivr mặc định sẽ bị chặn, thêm một lý do nữa để
  bắt buộc tự host.
- `video.srcObject = stream` không fetch URL nào, nên theo suy luận `media-src` không liên quan. Cần xác nhận trên máy
  thật cùng CSP production.

## 7. Vite PWA / Workbox

- Cấu hình hiện tại `injectManifest.globPatterns: ["**/*.{js,css,html,png,svg,woff2}"]` không có `wasm`, nên `.wasm`
  **đã** nằm ngoài precache. Lưu ý mặc định của Workbox là `**/*.{js,wasm,css,html}` và
  `maximumFileSizeToCacheInBytes` là 2 MiB, mà file wasm (947 KB) dưới ngưỡng này. Nếu ai đó thêm `wasm` vào pattern thì
  file sẽ bị precache ([workbox-build](https://developer.chrome.com/docs/workbox/modules/workbox-build)).
- Chunk JS thì **bị** precache theo `**/*.js`. Đã kiểm chứng: không có `globIgnores` thì manifest chứa chunk ponyfill.
  Có thì không:

```ts
injectManifest: {
  globPatterns: ["**/*.{js,css,html,png,svg,woff2}"],
  globIgnores: ["**/barcode.worker-*.js"], // hoặc "**/barcode-fallback-*.js" nếu không dùng worker
},
```

  Kết quả build thử: manifest chỉ còn `index.html`, `index-*.js`, `scan-page-*.js`. Chunk bị bỏ qua vẫn tải lười bình
  thường qua mạng, vì `precacheAndRoute` chỉ phục vụ URL có trong manifest, còn route `/api` NetworkOnly và
  NavigationRoute không đụng tới `/assets/*`. Không cần runtime caching: nginx đã gửi
  `Cache-Control: public, max-age=31536000, immutable` cho `/assets/`, nên HTTP cache giữ wasm giữa các lần truy cập.
- Đặt tên file có chủ đích (`barcode-fallback.ts` hoặc `barcode.worker.ts`) để glob ổn định. Đừng ignore theo tên
  package, vì Rollup có thể đổi tên chunk dùng chung.

## 8. Phát hiện ngoài câu hỏi nhưng ảnh hưởng thiết kế

1. **UPC trả về 13 chữ số trên đường WASM.** Từ zxing-cpp 3.0.0: "EAN/UPC: return 13-digit numbers for UPC-A/E as
   required by standard" ([release v3.0.0](https://github.com/zxing-cpp/zxing-cpp/releases)). Đo trên scratchpad với
   ponyfill 3.2.3: UPC-A `036000291452` cho ra `0036000291452`, và nếu bật cả `ean_13` thì định dạng bị báo là
   `ean_13`. UPC-E `01234565` cho ra `0012345000065`. Không có `ReaderOptions` nào để tắt hành vi này. API gốc trên
   Android (Play Services) nhiều khả năng trả nguyên dạng 12 hoặc 8 chữ số (chưa kiểm chứng trên máy). Trong khi đó
   server khớp `sku_barcodes.code` **chính xác** (`apps/api/internal/catalog/search.go:291-297`). **Cần quyết định:**
   chuẩn hóa về GTIN-13 ở một chỗ duy nhất, tốt nhất là phía server khi tra mã quét (thử cả dạng 12 và 13 chữ số), hoặc
   ở trang quét trước khi gửi. Hàng nội địa VN chủ yếu là EAN-13 (đầu 893) nên không bị ảnh hưởng. Hàng nhập từ Mỹ dùng
   UPC-A thì bị.
2. **Điện thoại sẽ đăng ký service worker và precache toàn bộ app POS.** `main.tsx:85` đăng ký SW trên mọi host tenant
   ở production, nên trang quét công khai tải về cả bundle mà nó không dùng. Nên bỏ qua `registerServiceWorker()` khi
   route là trang quét.
3. **`camera=()` hiện tại không chặn iOS,** vì Safari không hỗ trợ Permissions-Policy. Kiểm thử thay đổi header phải làm
   trên Chrome Android.

## Rủi ro áp dụng (tóm tắt)

| Hạng mục | Độ chín | Rủi ro |
|---|---|---|
| BarcodeDetector trên Chrome Android | Ổn định từ 2020 | Máy không có Play Services thì tự động dùng WASM |
| barcode-detector / zxing-wasm | Phát hành hằng tháng, API v3 ổn định từ 2/2026 | Một maintainer. Bản 3.2.3 mới 1 ngày tuổi |
| rVFC, OffscreenCanvas, `wasm-unsafe-eval` trên iOS | Có từ iOS 15.4/16/16.4 | Thấp với iOS 26/27 |
| Quyền camera trên iOS | Hỏi lại thường xuyên, chưa có bản sửa | Trải nghiệm kém. Chỉ giảm được bằng hướng dẫn |
| uqr | 0.1.x, ổn định | Thấp |

## Giới hạn của nghiên cứu

- Chưa thử trên máy thật: tốc độ và độ chính xác ponyfill trên iPhone (iOS 26/27), worker và `OffscreenCanvas` với ROI,
  định dạng chuỗi UPC từ API gốc trên Android, hành vi torch và zoom trên iOS. Kiểm thử round-trip trong Node chỉ chứng
  minh pipeline và phiên bản wasm khớp nhau, chưa chứng minh chất lượng đọc qua camera.
- Số liệu độ chính xác giữa các thư viện đều do nhà bán (Dynamsoft, STRICH, Scanbot) công bố, không độc lập.
- Không đánh giá SDK thương mại (Scandit, STRICH, Dynamsoft): ngoài ràng buộc của brainstorm và tốn phí license.

## Câu hỏi chưa giải quyết

1. Chuẩn hóa UPC/GTIN đặt ở server (tra cả dạng 12 và 13 chữ số) hay ở trang quét? Quyết định này ảnh hưởng hợp đồng
   API gửi mã.
2. Có chấp nhận sửa tiêu chí "điện thoại rung" thành "rung trên Android, bíp và nháy trên iOS" không?
3. Áp `'wasm-unsafe-eval'` toàn site hay chỉ cho route trang quét (thêm router Traefik)?
4. Bản đầu có dùng worker cho đường WASM không, hay chạy trên luồng chính rồi đo trên iPhone cũ nhất mà cửa hàng có?
   Cần biết model iPhone thấp nhất cần hỗ trợ.

Status: DONE_WITH_CONCERNS
Summary: Khuyến nghị `barcode-detector@3.2.3` + `zxing-wasm@3.1.6` (tự host wasm qua `?url` + `prepareZXingModule`, đã
kiểm chứng build Vite 8 và loại khỏi precache bằng `globIgnores`), `uqr@0.1.3` cho QR, CSP thêm `'wasm-unsafe-eval'`,
Permissions-Policy `camera=(self)`. iOS 26/27 vẫn không có BarcodeDetector và không có vibrate.
Concerns: UPC trả 13 chữ số trên đường WASM, cần chuẩn hóa GTIN. iOS không rung được nên phải sửa tiêu chí. SW sẽ
precache cả app trên điện thoại. Chưa thử trên máy thật.
