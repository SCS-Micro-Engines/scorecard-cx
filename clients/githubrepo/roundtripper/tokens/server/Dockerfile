# Copyright 2021 OpenSSF Scorecard Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

FROM golang:1.21.6@sha256:76aadd914a29a2ee7a6b0f3389bb2fdb87727291d688e1d972abe6c0fa6f2ee0 AS base
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.* ./
RUN go mod download
COPY . ./

FROM base AS authserver
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 make build-github-server

FROM gcr.io/distroless/base:nonroot@sha256:29da700a46816467c7cb91058f53eac4170a4a25ac8551d316d9fd38e2c58bdf
COPY --from=authserver /src/clients/githubrepo/roundtripper/tokens/server/github-auth-server clients/githubrepo/roundtripper/tokens/server/github-auth-server
ENTRYPOINT ["clients/githubrepo/roundtripper/tokens/server/github-auth-server"]
