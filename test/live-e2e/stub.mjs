// The provider stub of make live-e2e: it stands in for the fuel price and
// the timetable providers the live refresher's sources name, so the
// pre-release run never calls a provider. It serves HTTPS as provider.stub
// (the run's own throwaway certificate, /tls) on the stack's internal
// network, which reaches nothing outside the stack, and logs every request
// path (never a query string, which carries a key) for the run to check.
//
//   GET <base>/list.php?lat&lng&rad...  two open stations near the point
//   GET <base>/prices.php?ids=a,b       an open price for each id asked
//   GET <base>/plan/<eva>/<yymmdd>/<hh> one departure 30 minutes from now,
//                                       when it falls in the hour asked
//   GET <base>/fchg/<eva>               no changes
import { readFileSync } from "node:fs";
import { createServer } from "node:https";

const tls = { key: readFileSync("/tls/key.pem"), cert: readFileSync("/tls/cert.pem") };

// Berlin wall-clock parts of an instant, as the timetable provider writes them.
function berlin(at) {
  const parts = Object.fromEntries(
    new Intl.DateTimeFormat("en-GB", {
      timeZone: "Europe/Berlin", year: "2-digit", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23",
    }).formatToParts(at).map((p) => [p.type, p.value]),
  );
  return { date: parts.year + parts.month + parts.day, hour: parts.hour, minute: parts.minute };
}

function stationId(n) {
  return `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
}

function station(n, lat, lng) {
  return {
    id: stationId(n), name: `Stub station ${n}`, brand: "Stub", street: "Teststrasse", houseNumber: String(n), postCode: 10115,
    place: "Stubhausen", lat: lat + n / 1000, lng: lng + n / 1000, dist: n / 10, isOpen: true, e5: 1.789, e10: 1.729, diesel: 1.659,
  };
}

function json(res, body) {
  res.writeHead(200, { "content-type": "application/json; charset=utf-8" });
  res.end(JSON.stringify(body));
}

function xml(res, body) {
  res.writeHead(200, { "content-type": "application/xml; charset=utf-8" });
  res.end(`<?xml version="1.0" encoding="UTF-8"?>\n${body}`);
}

createServer(tls, (req, res) => {
  const url = new URL(req.url ?? "/", "https://provider.stub");
  // Printable ASCII only: a path never forges a line of the log the run reads.
  console.log(`stub ${req.method} ${url.pathname.replace(/[^\x21-\x7e]/g, "?")}`);
  let m;
  if (url.pathname.endsWith("/list.php")) {
    const lat = Number(url.searchParams.get("lat"));
    const lng = Number(url.searchParams.get("lng"));
    return json(res, { ok: true, status: "ok", stations: [station(1, lat, lng), station(2, lat, lng)] });
  }
  if (url.pathname.endsWith("/prices.php")) {
    const prices = {};
    for (const id of (url.searchParams.get("ids") ?? "").split(",").filter(Boolean)) {
      prices[id] = { status: "open", e5: 1.789, e10: 1.729, diesel: 1.659 };
    }
    return json(res, { ok: true, status: "ok", prices });
  }
  if ((m = /\/plan\/([0-9]{7})\/([0-9]{6})\/([0-9]{2})$/.exec(url.pathname))) {
    // The station number is echoed as a number, never as the request's text.
    const eva = String(Number(m[1])).padStart(7, "0");
    const [, , date, hour] = m;
    const at = berlin(new Date(Date.now() + 30 * 60 * 1000));
    const stop = at.date === date && at.hour === hour
      ? `<s id="stub-${date}${hour}-1"><tl c="ICE" n="1234" o="80"/><dp pt="${at.date}${at.hour}${at.minute}" pp="1" l="" ppth="Stubhausen|Teststadt"/></s>`
      : "";
    return xml(res, `<timetable station="Stub" eva="${eva}">${stop}</timetable>`);
  }
  if ((m = /\/fchg\/([0-9]{7})$/.exec(url.pathname))) {
    return xml(res, `<timetable station="Stub" eva="${String(Number(m[1])).padStart(7, "0")}"/>`);
  }
  res.writeHead(404, { "content-type": "text/plain" });
  res.end("not stubbed\n");
}).listen(443, () => console.log("stub listening on 443"));
