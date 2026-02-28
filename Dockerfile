FROM python:3.12-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY gaming_deals_bot/ gaming_deals_bot/

CMD ["python", "-m", "gaming_deals_bot"]
