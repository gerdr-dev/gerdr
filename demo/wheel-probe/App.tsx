// Probe for the web wheel change: a scroll view counting its onScrollEnd, with a short nested scroll view at its top.
import { mount, Scroll, Text, View, signal } from "nsh";

function App() {
  const ends = signal(0);
  const y = signal(0);
  const innerEnds = signal(0);
  return (
    <View style={{ flex: 1 }}>
      <Text testID="status">ends={ends()} y={y()} innerEnds={innerEnds()}</Text>
      <Scroll testID="outer" style={{ flex: 1 }} onScroll={(e) => y.set(Math.round(e.offset.y))} onScrollEnd={() => ends.set(ends() + 1)}>
        <Scroll testID="inner" style={{ height: 150, backgroundColor: "#ddd" }} onScrollEnd={() => innerEnds.set(innerEnds() + 1)}>
          {Array.from({ length: 20 }, (_, i) => <Text style={{ height: 30 }}>inner {i}</Text>)}
        </Scroll>
        {Array.from({ length: 200 }, (_, i) => <Text style={{ height: 40 }}>row {i}</Text>)}
      </Scroll>
    </View>
  );
}
mount(App);
