"use strict";
document.getElementById("print").addEventListener("click", () => window.print());
const copy = document.getElementById("copy");
if (copy) {
  copy.addEventListener("click", async () => {
    const text = document.getElementById("msg").innerText;
    try {
      await navigator.clipboard.writeText(text);
    } catch (e) { // older browsers or plain http: select the text instead
      const r = document.createRange();
      r.selectNodeContents(document.getElementById("msg"));
      const s = window.getSelection(); s.removeAllRanges(); s.addRange(r);
      document.execCommand("copy");
    }
    const old = copy.textContent;
    copy.textContent = copy.dataset.done;
    setTimeout(() => { copy.textContent = old; }, 1600);
  });
}
