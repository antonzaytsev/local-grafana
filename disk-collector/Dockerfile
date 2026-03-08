FROM python:3.12-alpine

RUN apk add --no-cache smartmontools

WORKDIR /app
COPY collector.py .

CMD ["python", "-u", "collector.py"]
