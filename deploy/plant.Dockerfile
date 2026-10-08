
# Planting: this repository is the template a new Seed grows from, and the
# seed CLI built here (with it embedded) plants it on first boot.
FROM go AS plant
WORKDIR /src
COPY . .
ARG SEED_VERSION=deploy
RUN echo "$SEED_VERSION" > kernel/VERSION \
 && date -u +%Y-%m-%dT%H:%M:%SZ > kernel/RELEASED \
 && mkdir -p kernel/template/assets \
 && tar --exclude=./kernel/template/assets/template.tar.gz -czf /tmp/template.tar.gz . \
 && mv /tmp/template.tar.gz kernel/template/assets/template.tar.gz \
 && GOFLAGS="-mod=readonly -buildvcs=false" go build -o /out/seed ./cmd/seed
