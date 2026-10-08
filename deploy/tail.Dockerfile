
# Deploy: a Seed for a platform. Mount a volume at /seed (an empty one is
# planted on first boot, named $SEED_NAME), pass DATABASE_URL for an external
# PostgreSQL (or none for the private one in the volume), and the secrets as
# environment variables. Listens on $PORT (default 8080); health: /_seed/healthz.
COPY --from=plant /out/seed /usr/local/bin/seed-plant
# An empty volume takes this directory's owner when first mounted.
RUN chown 1000:1000 /seed
USER 1000:1000
