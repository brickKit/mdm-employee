# The employee component alone: a static binary on Alpine (the health check runs through /bin/sh and wget).
FROM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /mdm-employee .

FROM alpine:3.24.2
COPY --from=build /mdm-employee /mdm-employee
# the listening port in a container; BrickKit injects PORT only for mode: local
ENV PORT=8227
EXPOSE 8227 9227
ENTRYPOINT ["/mdm-employee"]
