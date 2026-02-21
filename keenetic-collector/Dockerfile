FROM ruby:3.3-alpine

RUN apk add --no-cache git build-base

WORKDIR /app

COPY Gemfile ./
RUN bundle install --jobs 4 --retry 3

COPY collector.rb ./

CMD ["bundle", "exec", "ruby", "collector.rb"]
