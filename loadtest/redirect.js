// k6 load test for the redirect hot path — the latency-critical endpoint.
//
//   k6 run loadtest/redirect.js
//   BASE_URL=http://localhost:8080 k6 run loadtest/redirect.js
//
// setup() creates a short link once; the VUs then hammer GET /{code}, which after
// the first request is served entirely from the Redis cache (cache-aside).
import http from "k6/http";
import { check } from "k6";

const BASE_URL = __ENV.BASE_URL || "http://localhost:8080";

export const options = {
  scenarios: {
    redirect_hot_path: {
      executor: "ramping-vus",
      startVUs: 0,
      stages: [
        { duration: "5s", target: 50 },
        { duration: "20s", target: 50 },
        { duration: "5s", target: 0 },
      ],
    },
  },
  thresholds: {
    // Hot path served from cache should be fast and essentially never error.
    http_req_duration: ["p(95)<25", "p(99)<50"],
    http_req_failed: ["rate<0.01"],
  },
};

export function setup() {
  const res = http.post(
    `${BASE_URL}/shorten`,
    JSON.stringify({ url: "https://example.com/loadtest-target" }),
    { headers: { "Content-Type": "application/json" } }
  );
  check(res, { "setup created link": (r) => r.status === 201 });
  return { code: res.json("code") };
}

export default function (data) {
  // redirects: 0 so we measure the 302 itself, not the downstream fetch.
  const res = http.get(`${BASE_URL}/${data.code}`, { redirects: 0 });
  check(res, {
    "status is 302": (r) => r.status === 302,
    "has Location": (r) => !!r.headers["Location"],
  });
}
