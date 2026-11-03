// Is a hardware keyboard there to press shortcuts on? The guessing lives in @gerdr/chat/device (the chat cards need
// it too); this adds the Android App's exact answer: Configuration.hardKeyboardHidden, sent on hello (`keyboard`) and
// whenever a keyboard is plugged in / out (MainActivity.onConfigurationChanged → Bridge.keyboard).

import { setHwKeyboard } from "@gerdr/chat/device";
import { androidCan, onAndroid } from "./android";

onAndroid("keyboard", (m) => {
  if (androidCan("keyboard")) setHwKeyboard(m.hard === true);
});

export { hasHwKeyboard, useHwKeyboard } from "@gerdr/chat/device";
