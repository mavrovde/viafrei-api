// The owner console's passkey ceremonies: the server sends the options, the
// browser's authenticator answers them, and the answer goes back. Nothing
// here is stored; the session cookie is HttpOnly.
(function () {
  "use strict";
  var status = document.getElementById("status");
  function say(text) { status.textContent = text; }
  function bytes(s) {
    s = s.replace(/-/g, "+").replace(/_/g, "/");
    while (s.length % 4) { s += "="; }
    return Uint8Array.from(atob(s), function (c) { return c.charCodeAt(0); });
  }
  function text(buf) {
    var s = "", b = new Uint8Array(buf);
    for (var i = 0; i < b.length; i++) { s += String.fromCharCode(b[i]); }
    return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  }
  function post(path, body) {
    return fetch(path, {
      method: "POST", credentials: "same-origin",
      headers: { "Content-Type": "application/json" }, body: JSON.stringify(body)
    }).then(function (r) {
      return r.json().then(function (j) {
        if (!r.ok) { throw new Error(j.error || r.statusText); }
        return j;
      });
    });
  }
  function answer(cred, extra) {
    var out = { id: cred.id, rawId: text(cred.rawId), type: cred.type, response: {} };
    out.response.clientDataJSON = text(cred.response.clientDataJSON);
    for (var k in extra) { if (cred.response[extra[k]]) { out.response[extra[k]] = text(cred.response[extra[k]]); } }
    return out;
  }
  var signin = document.getElementById("signin");
  if (signin) {
    signin.addEventListener("click", function () {
      say("Waiting for your passkey…");
      post("/signin/begin", {}).then(function (b) {
        var o = b.options.publicKey;
        o.challenge = bytes(o.challenge);
        (o.allowCredentials || []).forEach(function (c) { c.id = bytes(c.id); });
        return navigator.credentials.get({ publicKey: o }).then(function (cred) {
          return post("/signin/finish", { id: b.id, credential: answer(cred, ["authenticatorData", "signature", "userHandle"]) });
        });
      }).then(function (r) { window.location.assign(r.next); }, function (e) { say("Sign-in failed: " + e.message); });
    });
  }
  var enrol = document.getElementById("enrol");
  if (enrol) {
    enrol.addEventListener("click", function () {
      say("Waiting for your authenticator…");
      post("/enrol/begin", { code: document.getElementById("code").value.trim() }).then(function (b) {
        var o = b.options.publicKey;
        o.challenge = bytes(o.challenge);
        o.user.id = bytes(o.user.id);
        (o.excludeCredentials || []).forEach(function (c) { c.id = bytes(c.id); });
        return navigator.credentials.create({ publicKey: o }).then(function (cred) {
          return post("/enrol/finish", { id: b.id, credential: answer(cred, ["attestationObject"]) });
        });
      }).then(function (r) { say("Passkey enrolled."); window.location.assign(r.next); }, function (e) { say("Enrolment failed: " + e.message); });
    });
  }
})();
